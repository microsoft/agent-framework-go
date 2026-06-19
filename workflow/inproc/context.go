// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
	"github.com/microsoft/agent-framework-go/workflow/internal/observability"
)

// runnerContext manages the execution context for a workflow run.
type runnerContext struct {
	wf        *workflow.Workflow
	sessionID string
	tracer    *stepTracer

	edgeMap  *execution.EdgeRunner
	nextStep atomic.Pointer[execution.StepContext]

	executors   map[string]*workflow.Executor
	executorsMu sync.RWMutex

	queuedExternalDeliveries []func(context.Context) error
	externalDeliveriesMu     sync.Mutex

	joinedSubworkflowRunners map[string]execution.SuperStepRunner
	joinedRunnersMu          sync.RWMutex

	externalRequests   map[string]*workflow.ExternalRequest
	requestOwners      map[string]string // requestID -> ownerExecutorID
	responsePortOwners map[string]string // portID -> ownerExecutorID
	requestsMu         sync.RWMutex

	stateManager   execution.StateManager
	outgoingEvents execution.EventSink

	withCheckpointing     bool
	concurrentRunsEnabled bool
	previousOwnership     any
	runEnded              atomic.Bool
}

// newInProcessRunnerContext creates a new InProcessRunnerContext.
func newInProcessRunnerContext(
	wf *workflow.Workflow,
	sessionID string,
	withCheckpointing bool,
	outgoingEvents execution.EventSink,
	tracer *stepTracer,
	existingOwnerSignoff any,
	enableConcurrentRuns bool,
) (*runnerContext, error) {
	ctx := &runnerContext{
		wf:                       wf,
		sessionID:                sessionID,
		tracer:                   tracer,
		executors:                make(map[string]*workflow.Executor),
		queuedExternalDeliveries: make([]func(context.Context) error, 0),
		joinedSubworkflowRunners: make(map[string]execution.SuperStepRunner),
		externalRequests:         make(map[string]*workflow.ExternalRequest),
		requestOwners:            make(map[string]string),
		responsePortOwners:       make(map[string]string),
		outgoingEvents:           outgoingEvents,
		withCheckpointing:        withCheckpointing,
		concurrentRunsEnabled:    enableConcurrentRuns,
		stateManager:             execution.NewStateManager(),
	}
	ctx.nextStep.Store(new(execution.StepContext))

	ctx.edgeMap = execution.NewEdgeRunner(wf, tracer, ctx.EnsureExecutor)
	if enableConcurrentRuns {
		if !wf.CheckOwnership(existingOwnerSignoff) {
			return nil, fmt.Errorf("existing ownership does not match check value")
		}
	} else {
		if err := wf.TakeOwnership(existingOwnerSignoff, ctx, false); err != nil {
			return nil, err
		}
		ctx.previousOwnership = existingOwnerSignoff
	}

	return ctx, nil
}

func (proc *runnerContext) currentStep() *execution.StepContext {
	nextStep := proc.nextStep.Load()
	if nextStep != nil {
		return nextStep
	}
	return new(execution.StepContext)
}

// checkEnded returns an error if the run has ended.
func (proc *runnerContext) checkEnded() error {
	if proc.runEnded.Load() {
		return fmt.Errorf("workflow run '%s' has been ended, please start a new Run or StreamingRun", proc.sessionID)
	}
	return nil
}

// EnsureExecutor ensures an executor is instantiated and initialized.
func (proc *runnerContext) EnsureExecutor(ctx context.Context, executorID string, tracer execution.StepTracer) (*workflow.Executor, error) {
	if err := proc.checkEnded(); err != nil {
		return nil, err
	}

	proc.executorsMu.RLock()
	if executor, ok := proc.executors[executorID]; ok {
		proc.executorsMu.RUnlock()
		return executor, nil
	}
	proc.executorsMu.RUnlock()

	// Need to create the executor
	proc.executorsMu.Lock()
	defer proc.executorsMu.Unlock()

	// Double-check after acquiring write lock
	if executor, ok := proc.executors[executorID]; ok {
		return executor, nil
	}

	registration, ok := proc.wf.ExecutorBinding(executorID)
	if !ok {
		return nil, fmt.Errorf("executor with ID '%s' is not registered", executorID)
	}

	executor, err := registration.CreateInstance(proc.sessionID)
	if err != nil {
		return nil, err
	}

	if err := executor.AttachRuntime(proc); err != nil {
		return nil, err
	}
	if err := executor.Initialize(proc.Bind(ctx, executorID, nil)); err != nil {
		return nil, err
	}

	if tracer != nil {
		tracer.TraceInstantiated(executorID)
	}

	proc.executors[executorID] = executor

	return executor, nil
}

func (proc *runnerContext) PrepareForCheckpoint(ctx context.Context) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}

	proc.executorsMu.RLock()
	executors := make([]*workflow.Executor, 0, len(proc.executors))
	for _, executor := range proc.executors {
		executors = append(executors, executor)
	}
	proc.executorsMu.RUnlock()

	for _, executor := range executors {
		if err := executor.OnCheckpoint(proc.Bind(ctx, executor.ID, nil)); err != nil {
			return err
		}
	}
	return nil
}

func (proc *runnerContext) NotifyCheckpointLoaded(ctx context.Context) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}

	proc.executorsMu.RLock()
	executors := make([]*workflow.Executor, 0, len(proc.executors))
	for _, executor := range proc.executors {
		executors = append(executors, executor)
	}
	proc.executorsMu.RUnlock()

	for _, executor := range executors {
		if err := executor.OnCheckpointRestored(proc.Bind(ctx, executor.ID, nil)); err != nil {
			return err
		}
	}
	return nil
}

func (proc *runnerContext) ExportState() checkpoint.RunnerStateData {
	proc.executorsMu.RLock()
	instantiated := make(map[string]struct{}, len(proc.executors))
	for id := range proc.executors {
		instantiated[id] = struct{}{}
	}
	proc.executorsMu.RUnlock()

	proc.requestsMu.RLock()
	outstanding := make([]*workflow.ExternalRequest, 0, len(proc.externalRequests))
	for _, request := range proc.externalRequests {
		outstanding = append(outstanding, request)
	}
	requestOwners := make(map[string]string, len(proc.requestOwners))
	for requestID, ownerID := range proc.requestOwners {
		requestOwners[requestID] = ownerID
	}
	responsePortOwners := make(map[string]string, len(proc.responsePortOwners))
	for portID, ownerID := range proc.responsePortOwners {
		responsePortOwners[portID] = ownerID
	}
	proc.requestsMu.RUnlock()

	return checkpoint.RunnerStateData{
		InstantiatedExecutors: instantiated,
		QueuedMessages:        proc.currentStep().ExportMessages(),
		OutstandingRequests:   outstanding,
		RequestOwners:         requestOwners,
		ResponsePortOwners:    responsePortOwners,
	}
}

func (proc *runnerContext) ImportState(ctx context.Context, cp *checkpoint.Checkpoint) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	if cp == nil {
		return errors.New("checkpoint cannot be nil")
	}

	proc.executorsMu.Lock()
	proc.executors = make(map[string]*workflow.Executor, len(cp.RunnerData.InstantiatedExecutors))
	proc.executorsMu.Unlock()

	proc.joinedRunnersMu.Lock()
	proc.joinedSubworkflowRunners = make(map[string]execution.SuperStepRunner)
	proc.joinedRunnersMu.Unlock()

	for executorID := range cp.RunnerData.InstantiatedExecutors {
		if _, err := proc.EnsureExecutor(ctx, executorID, nil); err != nil {
			return err
		}
	}

	proc.externalDeliveriesMu.Lock()
	proc.queuedExternalDeliveries = make([]func(context.Context) error, 0)
	proc.externalDeliveriesMu.Unlock()

	nextStep := new(execution.StepContext)
	nextStep.ImportMessages(cp.RunnerData.QueuedMessages)
	proc.nextStep.Store(nextStep)

	proc.requestsMu.Lock()
	proc.externalRequests = make(map[string]*workflow.ExternalRequest, len(cp.RunnerData.OutstandingRequests))
	proc.requestOwners = make(map[string]string, len(cp.RunnerData.RequestOwners))
	proc.responsePortOwners = make(map[string]string, len(cp.RunnerData.ResponsePortOwners))
	for requestID, ownerID := range cp.RunnerData.RequestOwners {
		proc.requestOwners[requestID] = ownerID
	}
	for portID, ownerID := range cp.RunnerData.ResponsePortOwners {
		proc.responsePortOwners[portID] = ownerID
	}
	for _, request := range cp.RunnerData.OutstandingRequests {
		proc.externalRequests[request.RequestID] = request
		if ownerID := proc.requestOwners[request.RequestID]; ownerID == "" {
			ownerID = proc.responsePortOwners[request.PortInfo.PortID]
			if ownerID == "" {
				ownerID = request.PortInfo.PortID
			}
			proc.requestOwners[request.RequestID] = ownerID
		}
		if _, ok := proc.responsePortOwners[request.PortInfo.PortID]; !ok {
			proc.responsePortOwners[request.PortInfo.PortID] = proc.requestOwners[request.RequestID]
		}
	}
	proc.requestsMu.Unlock()

	return nil
}

// AddExternalMessage queues an external message for delivery.
func (proc *runnerContext) AddExternalMessage(ctx context.Context, message any, declaredType reflect.Type) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	if message == nil {
		return errors.New("message cannot be nil")
	}

	proc.externalDeliveriesMu.Lock()
	defer proc.externalDeliveriesMu.Unlock()
	proc.queuedExternalDeliveries = append(proc.queuedExternalDeliveries, func(ctx context.Context) error {
		envelope, err := execution.NewMessageEnvelope(message, declaredType, "", "")
		if err != nil {
			return err
		}
		mapping, err := proc.edgeMap.PrepareDeliveryForInput(ctx, envelope)
		if err != nil {
			return err
		}

		if mapping != nil {
			mapping.MapInto(proc.currentStep())
		}
		return nil
	})
	return nil
}

// AddExternalResponse queues an external response for delivery.
func (proc *runnerContext) AddExternalResponse(ctx context.Context, response *workflow.ExternalResponse) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}

	proc.externalDeliveriesMu.Lock()
	defer proc.externalDeliveriesMu.Unlock()
	proc.queuedExternalDeliveries = append(proc.queuedExternalDeliveries, func(ctx context.Context) error {
		ownerID, existed := proc.CompleteRequest(response.RequestID)
		if !existed {
			return fmt.Errorf("no pending request with ID %s found in the workflow context", response.RequestID)
		}

		mapping, err := proc.edgeMap.PrepareDeliveryForResponse(ctx, response, ownerID)
		if err != nil {
			return err
		}

		if mapping != nil {
			mapping.MapInto(proc.currentStep())
		}
		return nil
	})
	return nil
}

// HasQueuedExternalDeliveries returns true if there are queued external deliveries.
func (proc *runnerContext) HasQueuedExternalDeliveries() bool {
	proc.externalDeliveriesMu.Lock()
	defer proc.externalDeliveriesMu.Unlock()
	return len(proc.queuedExternalDeliveries) > 0
}

// JoinedRunnersHaveActions returns true if any joined subworkflow has actions.
func (proc *runnerContext) JoinedRunnersHaveActions() bool {
	proc.joinedRunnersMu.RLock()
	defer proc.joinedRunnersMu.RUnlock()

	for _, runner := range proc.joinedSubworkflowRunners {
		if runner.HasUnprocessedMessages() {
			return true
		}
	}
	return false
}

// NextStepHasActions returns true if the next step has actions to process.
func (proc *runnerContext) NextStepHasActions() bool {
	return proc.currentStep().HasMessages() || proc.HasQueuedExternalDeliveries() || proc.JoinedRunnersHaveActions()
}

// HasUnservicedRequests returns true if there are unserviced external requests.
func (proc *runnerContext) HasUnservicedRequests() bool {
	proc.requestsMu.RLock()
	hasLocal := len(proc.externalRequests) > 0
	proc.requestsMu.RUnlock()

	if hasLocal {
		return true
	}

	proc.joinedRunnersMu.RLock()
	defer proc.joinedRunnersMu.RUnlock()

	for _, runner := range proc.joinedSubworkflowRunners {
		if runner.HasUnservicedRequests() {
			return true
		}
	}
	return false
}

// RepublishUnservicedRequests re-emits outstanding external requests after an
// event stream has subscribed, matching checkpoint-resume behavior.
func (proc *runnerContext) RepublishUnservicedRequests(ctx context.Context) error {
	proc.requestsMu.RLock()
	requestIDs := make([]string, 0, len(proc.externalRequests))
	for requestID := range proc.externalRequests {
		requestIDs = append(requestIDs, requestID)
	}
	slices.Sort(requestIDs)
	requests := make([]*workflow.ExternalRequest, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		requests = append(requests, proc.externalRequests[requestID])
	}
	proc.requestsMu.RUnlock()

	for _, request := range requests {
		if err := proc.AddEvent(ctx, workflow.RequestInfoEvent{Request: request}); err != nil {
			return err
		}
	}
	return nil
}

// Advance advances to the next step, processing all queued external deliveries.
func (proc *runnerContext) Advance(ctx context.Context) (*execution.StepContext, error) {
	if err := proc.checkEnded(); err != nil {
		return nil, err
	}

	// Process all queued external deliveries
	proc.externalDeliveriesMu.Lock()
	deliveries := proc.queuedExternalDeliveries
	proc.queuedExternalDeliveries = make([]func(context.Context) error, 0)
	proc.externalDeliveriesMu.Unlock()

	for _, delivery := range deliveries {
		if err := delivery(ctx); err != nil {
			return nil, err
		}
	}

	// Swap out the next step
	return proc.nextStep.Swap(new(execution.StepContext)), nil
}

// AddEvent adds a workflow event to the outgoing event stream.
func (proc *runnerContext) AddEvent(ctx context.Context, event workflow.Event) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	return proc.outgoingEvents.Enqueue(ctx, event)
}

// SendMessage sends a message from one executor to another through the workflow edges.
func (proc *runnerContext) SendMessage(ctx context.Context, sourceID, targetID string, message any) error {
	telemetry := observability.FromContext(ctx)
	ctx, span := telemetry.StartMessageSend(ctx, sourceID, targetID, message)
	defer span.End()

	if err := proc.checkEnded(); err != nil {
		span.CaptureError(err)
		return err
	}
	messageType := reflect.TypeOf(message)
	if messageType == nil {
		err := fmt.Errorf("executor %q cannot send nil", sourceID)
		span.CaptureError(err)
		return err
	}
	sourceExecutor, err := proc.EnsureExecutor(ctx, sourceID, nil)
	if err != nil {
		span.CaptureError(err)
		return err
	}
	declaredType, ok := execution.DeclaredSendType(sourceExecutor, messageType)
	if !ok {
		err := fmt.Errorf("executor %q cannot send messages of type %s", sourceID, messageType)
		span.CaptureError(err)
		return err
	}

	envelope, err := execution.NewMessageEnvelope(message, declaredType, sourceID, targetID)
	if err != nil {
		span.CaptureError(err)
		return err
	}
	if traceContext := telemetry.ExtractTraceContext(ctx); len(traceContext) > 0 {
		envelope.TraceContext = traceContext
	}
	edges := proc.wf.OutgoingEdges(sourceID)
	for _, edge := range edges {
		mapping, err := proc.edgeMap.PrepareDeliveryForEdge(ctx, edge, envelope)
		if err != nil {
			span.CaptureError(err)
			return err
		}
		if mapping != nil {
			mapping.MapInto(proc.currentStep())
		}
	}

	return nil
}

// Bind creates a bound workflow context for a specific executor.
func (proc *runnerContext) Bind(ctx context.Context, executorID string, traceContext map[string]string) *workflow.Context {
	boundCtx := ctx
	telemetry := observability.FromContext(ctx)
	boundCtx = observability.ContextWithTelemetry(boundCtx, telemetry)

	return &workflow.Context{
		Context: boundCtx,

		AddEvent: func(event workflow.Event) error {
			return proc.AddEvent(boundCtx, event)
		},

		SendMessage: func(targetID string, message any) error {
			return proc.SendMessage(boundCtx, executorID, targetID, message)
		},

		YieldOutput: func(output any) error {
			if err := proc.validateOutputType(executorID, output); err != nil {
				return err
			}
			if proc.wf.HasOutputExecutor(executorID) {
				return proc.AddEvent(boundCtx, workflow.OutputEvent{
					ExecutorID: executorID,
					Output:     output,
				})
			}
			return nil
		},

		RequestHalt: func() error {
			return proc.AddEvent(boundCtx, workflow.RequestHaltEvent{})
		},

		PostRequest: func(request *workflow.ExternalRequest) error {
			return proc.Post(boundCtx, executorID, request)
		},

		ReadState: func(key string, scope string) (any, error) {
			value, ok, err := proc.stateManager.ReadState(executorID, scope, key)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, nil
			}
			return value.Any(), nil
		},

		ReadOrInitState: func(key string, scope string, initFunc func(context.Context, string, string) (any, error)) (any, error) {
			var initErr error
			value, err := proc.stateManager.ReadOrInitState(executorID, scope, key, func() any {
				// Call the init function to get the initial value
				if initFunc != nil {
					val, err := initFunc(boundCtx, key, scope)
					if err != nil {
						initErr = err
						return nil
					}
					return val
				}
				return nil
			})
			if initErr != nil {
				return nil, initErr
			}
			if err != nil {
				return nil, err
			}
			return value.Any(), nil
		},

		ReadStateKeys: func(scope string) iter.Seq2[string, error] {
			return func(yield func(string, error) bool) {
				keys := proc.stateManager.ReadKeys(executorID, scope)
				for key := range keys {
					if !yield(key, nil) {
						return
					}
				}
			}
		},

		QueueStateUpdate: func(key string, scope string, value any) error {
			return proc.stateManager.WriteState(executorID, scope, key, value)
		},

		TraceContext: func() map[string]any {
			if traceContext == nil {
				return nil
			}
			// Convert map[string]string to map[string]any
			result := make(map[string]any, len(traceContext))
			for k, v := range traceContext {
				result[k] = v
			}
			return result
		},

		ConcurrentRunsEnabled: proc.concurrentRunsEnabled,
	}
}

func (proc *runnerContext) validateOutputType(executorID string, output any) error {
	if output == nil {
		return fmt.Errorf("executor %q cannot output nil", executorID)
	}

	proc.executorsMu.RLock()
	executor, ok := proc.executors[executorID]
	proc.executorsMu.RUnlock()
	if !ok {
		return fmt.Errorf("executor %q is not instantiated", executorID)
	}

	declaredTypes := executor.DescribeProtocol().Yields
	outputType := reflect.TypeOf(output)
	if execution.CanOutputType(executor, outputType) {
		return nil
	}
	return fmt.Errorf("executor %q cannot output object of type %s; expected one of %v", executorID, outputType, sortedTypeNames(declaredTypes))
}

func sortedTypeNames(types []reflect.Type) []string {
	names := make([]string, 0, len(types))
	for _, typ := range types {
		names = append(names, typ.String())
	}
	slices.Sort(names)
	return names
}

// Post raises an external request originating from the executor identified
// by ownerID. The matching response (delivered later via [AddExternalResponse])
// will be routed back to that executor as a regular message.
func (proc *runnerContext) Post(ctx context.Context, ownerID string, request *workflow.ExternalRequest) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	if ownerID == "" {
		return errors.New("ownerID is required when posting an external request")
	}

	proc.requestsMu.Lock()
	if _, exists := proc.externalRequests[request.RequestID]; exists {
		proc.requestsMu.Unlock()
		return fmt.Errorf("pending request with id '%s' already exists", request.RequestID)
	}
	proc.externalRequests[request.RequestID] = request
	proc.requestOwners[request.RequestID] = ownerID
	proc.responsePortOwners[request.PortInfo.PortID] = ownerID
	proc.requestsMu.Unlock()

	return proc.AddEvent(ctx, workflow.RequestInfoEvent{Request: request})
}

// CompleteRequest marks a request as completed and returns the executor that
// posted it.
func (proc *runnerContext) CompleteRequest(requestID string) (string, bool) {
	if err := proc.checkEnded(); err != nil {
		return "", false
	}

	proc.requestsMu.Lock()
	defer proc.requestsMu.Unlock()

	_, existed := proc.externalRequests[requestID]
	delete(proc.externalRequests, requestID)
	owner := proc.requestOwners[requestID]
	delete(proc.requestOwners, requestID)
	return owner, existed
}

// ResponsePortExecutorID returns the executor that handles responses on the
// given port, or ("", false) if no such port is registered. Mirrors .NET's
// [EdgeMap.TryGetResponsePortExecutorId].
func (proc *runnerContext) ResponsePortExecutorID(portID string) (string, bool) {
	proc.requestsMu.Lock()
	defer proc.requestsMu.Unlock()
	owner, ok := proc.responsePortOwners[portID]
	return owner, ok
}

// JoinedSubworkflowRunners returns all joined subworkflow runners.
func (proc *runnerContext) JoinedSubworkflowRunners() []execution.SuperStepRunner {
	proc.joinedRunnersMu.RLock()
	defer proc.joinedRunnersMu.RUnlock()

	runners := make([]execution.SuperStepRunner, 0, len(proc.joinedSubworkflowRunners))
	for _, runner := range proc.joinedSubworkflowRunners {
		runners = append(runners, runner)
	}
	return runners
}

// AttachSuperstep attaches a subworkflow runner.
func (proc *runnerContext) AttachSuperstep(ctx context.Context, runner execution.SuperStepRunner) (string, error) {
	proc.joinedRunnersMu.Lock()
	defer proc.joinedRunnersMu.Unlock()

	// Generate a unique join ID
	joinID := uuid.NewString()
	proc.joinedSubworkflowRunners[joinID] = runner
	return joinID, nil
}

// DetachSuperstep detaches a subworkflow runner.
func (proc *runnerContext) DetachSuperstep(joinID string) bool {
	proc.joinedRunnersMu.Lock()
	defer proc.joinedRunnersMu.Unlock()

	_, existed := proc.joinedSubworkflowRunners[joinID]
	delete(proc.joinedSubworkflowRunners, joinID)
	return existed
}

// EndRun marks the run as ended and cleans up resources.
func (proc *runnerContext) EndRun(ctx context.Context) error {
	if proc.runEnded.Swap(true) {
		return nil // Already ended
	}
	var runErr error

	proc.executorsMu.RLock()
	executors := make([]*workflow.Executor, 0, len(proc.executors))
	for _, executor := range proc.executors {
		executors = append(executors, executor)
	}
	proc.executorsMu.RUnlock()

	for _, executor := range executors {
		if err := executor.Close(ctx); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}

	proc.joinedRunnersMu.Lock()
	joinedRunners := make([]execution.SuperStepRunner, 0, len(proc.joinedSubworkflowRunners))
	for _, runner := range proc.joinedSubworkflowRunners {
		joinedRunners = append(joinedRunners, runner)
	}
	proc.joinedSubworkflowRunners = make(map[string]execution.SuperStepRunner)
	proc.joinedRunnersMu.Unlock()

	for _, runner := range joinedRunners {
		if err := runner.RequestEndRun(ctx); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	if !proc.concurrentRunsEnabled {
		// Release workflow ownership
		if err := proc.wf.ReleaseOwnershipTo(proc, proc.previousOwnership); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	return runErr
}
