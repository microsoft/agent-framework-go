// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/microsoft/agent-framework-go/internal/concurrent"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	internalcheckpoint "github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

const (
	checkpointManagerStateKey    = "workflow.Subworkflow.CheckpointManager"
	pendingResponsePortsStateKey = "workflow.Subworkflow.PendingResponsePorts"
	subworkflowImplementationID  = "workflow/subworkflow.Binding"
)

// BindSubworkflowAsExecutor returns an [workflow.ExecutorBinding] that hosts wf
// as a subworkflow with the supplied workflow-unique executor ID.
func BindSubworkflowAsExecutor(wf *workflow.Workflow, id string) workflow.ExecutorBinding {
	if wf == nil {
		panic("subworkflow: cannot bind nil Workflow as executor")
	}
	if id == "" {
		panic("subworkflow: executor ID cannot be empty")
	}
	ownershipToken := new(struct{})
	if err := wf.TakeOwnership(nil, ownershipToken, true); err != nil {
		panic(fmt.Sprintf("subworkflow: cannot bind Workflow as executor %q: %v", id, err))
	}
	return workflow.ExecutorBinding{
		ID:                                id,
		ImplementationID:                  subworkflowImplementationID,
		RawValue:                          wf,
		SupportsConcurrentSharedExecution: true,
		NewExecutorFunc: func(sessionID string) (*workflow.Executor, error) {
			return newSubworkflowExecutor(id, wf, sessionID, ownershipToken)
		},
	}
}

func newSubworkflowExecutor(id string, wf *workflow.Workflow, sessionID string, ownershipToken any) (*workflow.Executor, error) {
	protocol, err := wf.DescribeProtocol()
	if err != nil {
		return nil, err
	}
	host := &subworkflowHostExecutor{
		id:             id,
		wf:             wf,
		sessionID:      sessionID,
		ownershipToken: ownershipToken,
		protocol:       protocol,
	}
	return host.executor(), nil
}

type subworkflowHostExecutor struct {
	id             string
	wf             *workflow.Workflow
	sessionID      string
	ownershipToken any
	protocol       workflow.ProtocolDescriptor
	joinContext    *runnerContext

	runner               *runner
	run                  *execution.RunHandle
	joinID               string
	eventHandler         func(context.Context, any, workflow.Event) error
	checkpointManager    checkpoint.Manager
	pendingResponsePorts concurrent.Map[string, workflow.RequestPortInfo]
}

func (h *subworkflowHostExecutor) executor() *workflow.Executor {
	return &workflow.Executor{
		ID:               h.id,
		ImplementationID: subworkflowImplementationID,
		DisableAutoSendMessageHandlerResultObject: true,
		DisableAutoYieldOutputHandlerResultObject: true,
		ConfigureProtocol:                         h.configureProtocol,
		AttachRuntimeFunc:                         h.attachRuntime,
		CloseFunc:                                 h.reset,
		OnCheckpointFunc:                          h.onCheckpoint,
		OnCheckpointRestoredFunc:                  h.onCheckpointRestored,
	}
}

func (h *subworkflowHostExecutor) attachRuntime(runtime any) error {
	joinContext, ok := runtime.(*runnerContext)
	if !ok {
		return errors.New("subworkflow: current execution environment does not support subworkflows")
	}
	h.joinContext = joinContext
	return nil
}

func (h *subworkflowHostExecutor) ensureRunner() (*runner, error) {
	if h.runner != nil {
		return h.runner, nil
	}
	if h.joinContext == nil {
		return nil, errors.New("subworkflow: current execution environment does not support subworkflows")
	}
	if h.checkpointManager == nil && h.joinContext.withCheckpointing {
		// Use a separate in-memory checkpoint manager for scoping purposes. We do not need to worry about
		// serialization because we will be relying on the parent workflow's checkpoint manager to do that,
		// if needed. For our purposes, all we need is to keep a faithful representation of the checkpointed
		// objects so we can emit them back to the parent workflow on checkpoint creation.
		h.checkpointManager = checkpoint.NewInMemoryManager()
	}
	checkpointMgr, err := subworkflowCheckpointManager(h.checkpointManager)
	if err != nil {
		return nil, err
	}
	runner, err := createSubworkflowRunner(h.wf, checkpointMgr, h.sessionID, h.ownershipToken, h.joinContext.concurrentRunsEnabled, nil)
	if err != nil {
		return nil, err
	}
	h.runner = runner
	return runner, nil
}

func (h *subworkflowHostExecutor) beginRun(ctx *workflow.Context, runner *runner, incomingMessage any, resume bool) (*execution.RunHandle, error) {
	if h.checkpointManager != nil {
		if resume {
			checkpointInfo, err := lastCheckpoint(ctx, runner.checkpointMgr, runner.sessionID)
			if err != nil {
				return nil, err
			}
			if checkpointInfo == nil {
				return nil, errors.New("workflow: no subworkflow checkpoints available to resume from")
			}
			return runner.resumeStreamWithRepublish(ctx, execution.ModeSubworkflow, *checkpointInfo, true)
		}
		if incomingMessage == nil {
			return nil, errors.New("subworkflow: cannot start a checkpointed workflow run without an incoming message or resume flag")
		}
		return runner.beginStream(ctx, execution.ModeSubworkflow)
	}
	if incomingMessage == nil {
		return nil, errors.New("subworkflow: cannot start workflow run without an incoming message")
	}
	return runner.beginStream(ctx, execution.ModeSubworkflow)
}

func (h *subworkflowHostExecutor) ensureRunSendMessage(ctx *workflow.Context, incomingMessage any, resume bool) (*execution.RunHandle, error) {
	if h.run != nil {
		if incomingMessage != nil {
			if err := h.run.EnqueueMessage(ctx, incomingMessage); err != nil {
				return nil, err
			}
		}
		return h.run, nil
	}

	runner, err := h.ensureRunner()
	if err != nil {
		return nil, err
	}

	run, err := h.beginRun(ctx, runner, incomingMessage, resume)
	if err != nil {
		_ = runner.RequestEndRun(ctx)
		return nil, err
	}
	if incomingMessage != nil {
		if err := run.EnqueueMessage(ctx, incomingMessage); err != nil {
			_ = run.Close(ctx)
			_ = runner.RequestEndRun(ctx)
			return nil, err
		}
	}

	joinID, err := h.joinContext.AttachSuperstep(ctx, runner)
	if err != nil {
		_ = run.Close(ctx)
		_ = runner.RequestEndRun(ctx)
		return nil, err
	}

	eventHandler := func(_ context.Context, _ any, evt workflow.Event) error {
		return h.forwardWorkflowEvent(ctx, evt)
	}
	runner.OutgoingEvents().AddHandler(eventHandler)

	h.run = run
	h.joinID = joinID
	h.eventHandler = eventHandler
	return h.run, nil
}

func (h *subworkflowHostExecutor) configureProtocol(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
	pb.SendsMessageType(h.protocol.Yields...)
	pb.YieldsOutputType(h.protocol.Yields...)
	pb.RouteBuilder.AddCatchAll(h.queueExternalMessage)
	return pb, nil
}

func (h *subworkflowHostExecutor) queueExternalMessage(ctx *workflow.Context, msg workflow.PortableValue) (any, error) {
	if response, ok := workflow.PortableValueAs[*workflow.ExternalResponse](msg); ok {
		unqualified, ok := h.checkAndUnqualifyResponse(response)
		if !ok {
			return nil, nil
		}
		_, err := h.ensureRunSendMessage(ctx, unqualified, false)
		return nil, err
	}

	for _, typ := range h.protocol.Accepts {
		if value, ok := msg.As(typ); ok {
			_, err := h.ensureRunSendMessage(ctx, value, false)
			return nil, err
		}
	}

	return nil, nil
}

func (h *subworkflowHostExecutor) forwardWorkflowEvent(ctx *workflow.Context, evt workflow.Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if recovered, ok := r.(error); ok {
				err = recovered
			} else {
				err = fmt.Errorf("panic: %v", r)
			}
		}
		if err != nil {
			if h.joinContext != nil {
				_ = h.joinContext.AddEvent(ctx, workflow.ErrorEvent{Error: err, SubWorkflowID: h.id})
			}
			err = nil
		}
	}()

	switch event := evt.(type) {
	case workflow.StartedEvent, workflow.SuperStepStartedEvent, workflow.SuperStepCompletedEvent:
		return nil
	case workflow.RequestInfoEvent:
		request := h.qualifyRequestPortID(event.Request)
		if request == nil {
			return nil
		}
		if h.joinContext != nil {
			return h.joinContext.SendMessage(ctx, h.id, "", request)
		}
		return nil
	case workflow.ErrorEvent:
		event.SubWorkflowID = h.id
		if h.joinContext != nil {
			return h.joinContext.AddEvent(ctx, event)
		}
		return nil
	case workflow.OutputEvent:
		if event.Output == nil {
			return nil
		}
		if h.joinContext != nil {
			if err := h.joinContext.SendMessage(ctx, h.id, "", event.Output); err != nil {
				return err
			}
			return h.yieldOutput(ctx, event.Output)
		}
		return nil
	case workflow.RequestHaltEvent:
		if h.joinContext != nil {
			return h.joinContext.AddEvent(ctx, workflow.RequestHaltEvent{})
		}
		return nil
	default:
		if h.joinContext != nil {
			return h.joinContext.AddEvent(ctx, evt)
		}
		return nil
	}
}

func (h *subworkflowHostExecutor) yieldOutput(ctx context.Context, output any) error {
	return h.joinContext.yieldOutput(ctx, h.id, output)
}

func (h *subworkflowHostExecutor) qualifyRequestPortID(request *workflow.ExternalRequest) *workflow.ExternalRequest {
	if request == nil {
		return nil
	}
	h.pendingResponsePorts.Store(request.RequestID, request.PortInfo)
	qualified := *request
	qualified.PortInfo = request.PortInfo
	qualified.PortInfo.PortID = h.id + "." + request.PortInfo.PortID
	return &qualified
}

func (h *subworkflowHostExecutor) checkAndUnqualifyResponse(response *workflow.ExternalResponse) (*workflow.ExternalResponse, bool) {
	if response == nil {
		return nil, false
	}
	if original, ok := h.pendingResponsePorts.LoadAndDelete(response.RequestID); ok {
		unqualified := *response
		unqualified.PortInfo = original
		return &unqualified, true
	}

	prefix := h.id + "."
	if !strings.HasPrefix(response.PortInfo.PortID, prefix) {
		return nil, false
	}
	unqualified := *response
	unqualified.PortInfo = response.PortInfo
	unqualified.PortInfo.PortID = strings.TrimPrefix(response.PortInfo.PortID, h.id+".")
	return &unqualified, true
}

func (h *subworkflowHostExecutor) onCheckpoint(ctx *workflow.Context) error {
	pending := maps.Collect(h.pendingResponsePorts.All())
	if err := ctx.QueueStateUpdate(checkpointManagerStateKey, "", h.checkpointManager); err != nil {
		return err
	}
	return ctx.QueueStateUpdate(pendingResponsePortsStateKey, "", pending)
}

func (h *subworkflowHostExecutor) onCheckpointRestored(ctx *workflow.Context) error {
	checkpointManager, err := readStateAs[checkpoint.Manager](ctx, checkpointManagerStateKey)
	if err != nil {
		return err
	}
	if checkpointManager == nil {
		checkpointManager = checkpoint.NewInMemoryManager()
	}

	if h.checkpointManager != checkpointManager {
		h.checkpointManager = checkpointManager
		if err := h.reset(ctx); err != nil {
			return err
		}
	}

	pending, err := readStateAs[map[string]workflow.RequestPortInfo](ctx, pendingResponsePortsStateKey)
	if err != nil {
		return err
	}

	h.pendingResponsePorts.Clear()
	for requestID, portInfo := range pending {
		h.pendingResponsePorts.Store(requestID, portInfo)
	}

	_, err = h.ensureRunSendMessage(ctx, nil, true)
	return err
}

func (h *subworkflowHostExecutor) reset(ctx context.Context) error {
	var runErr error
	if h.run != nil {
		if err := h.run.Close(ctx); err != nil {
			runErr = errors.Join(runErr, err)
		}
		h.run = nil
	}

	h.pendingResponsePorts.Clear()

	if h.runner != nil {
		if h.eventHandler != nil {
			h.runner.OutgoingEvents().RemoveHandler(h.eventHandler)
			h.eventHandler = nil
		}
		if err := h.runner.RequestEndRun(ctx); err != nil {
			runErr = errors.Join(runErr, err)
		}
		h.runner = nil
	}

	if h.joinContext != nil && h.joinID != "" {
		h.joinContext.DetachSuperstep(h.joinID)
		h.joinID = ""
	}
	return runErr
}

func readStateAs[T any](ctx *workflow.Context, key string) (T, error) {
	var zero T
	if ctx.ReadState == nil {
		return zero, nil
	}
	value, err := ctx.ReadState(key, "")
	if err != nil {
		return zero, err
	}
	if value == nil {
		return zero, nil
	}
	typed, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("subworkflow: state %q has type %T, want %v", key, value, reflect.TypeFor[T]())
	}
	return typed, nil
}

func subworkflowCheckpointManager(manager checkpoint.Manager) (internalcheckpoint.Manager, error) {
	if manager == nil {
		return nil, nil
	}
	mgr, ok := manager.(internalcheckpoint.Manager)
	if !ok {
		return nil, fmt.Errorf("workflow: subworkflow checkpoint manager has type %T, want internal checkpoint.Manager", manager)
	}
	return mgr, nil
}

func lastCheckpoint(ctx context.Context, mgr internalcheckpoint.Manager, sessionID string) (*workflow.CheckpointInfo, error) {
	if mgr == nil || sessionID == "" {
		return nil, nil
	}
	index, err := mgr.RetrieveIndex(ctx, sessionID, nil)
	if err != nil {
		return nil, err
	}
	if len(index) == 0 {
		return nil, nil
	}
	last := index[len(index)-1]
	return &last, nil
}
