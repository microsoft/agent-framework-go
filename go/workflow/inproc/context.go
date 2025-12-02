// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework/go/workflow"
	"github.com/microsoft/agent-framework/go/workflow/internal/execution"
)

// runnerContext manages the execution context for a workflow run.
type runnerContext struct {
	wf     *workflow.Workflow
	runID  string
	tracer *stepTracer

	edgeMap  *execution.EdgeRunner
	nextStep *execution.StepContext

	executors   map[string]*workflow.Executor
	executorsMu sync.RWMutex

	queuedExternalDeliveries []func(context.Context) error
	externalDeliveriesMu     sync.Mutex

	joinedSubworkflowRunners map[string]execution.SuperStepRunner
	joinedRunnersMu          sync.RWMutex

	externalRequests map[string]*workflow.ExternalRequest
	requestsMu       sync.RWMutex

	stateManager   execution.StateManager
	outgoingEvents execution.EventSink

	withCheckpointing     bool
	concurrentRunsEnabled bool
	runEnded              atomic.Bool
}

// newInProcessRunnerContext creates a new InProcessRunnerContext.
func newInProcessRunnerContext(
	wf *workflow.Workflow,
	runID string,
	withCheckpointing bool,
	outgoingEvents execution.EventSink,
	tracer *stepTracer,
	existingOwnerSignoff any,
	enableConcurrentRuns bool,
) (*runnerContext, error) {

	ctx := &runnerContext{
		wf:                       wf,
		runID:                    runID,
		tracer:                   tracer,
		nextStep:                 new(execution.StepContext),
		executors:                make(map[string]*workflow.Executor),
		queuedExternalDeliveries: make([]func(context.Context) error, 0),
		joinedSubworkflowRunners: make(map[string]execution.SuperStepRunner),
		externalRequests:         make(map[string]*workflow.ExternalRequest),
		outgoingEvents:           outgoingEvents,
		withCheckpointing:        withCheckpointing,
		concurrentRunsEnabled:    enableConcurrentRuns,
	}

	ctx.edgeMap = execution.NewEdgeRunner(wf, tracer, ctx.EnsureExecutor)
	if enableConcurrentRuns {
		wf.CheckOwnership(existingOwnerSignoff)
	} else {
		if err := wf.TakeOwnership(existingOwnerSignoff, ctx, false); err != nil {
			return nil, err
		}
	}

	return ctx, nil
}

// checkEnded returns an error if the run has ended.
func (proc *runnerContext) checkEnded() error {
	if proc.runEnded.Load() {
		return fmt.Errorf("workflow run '%s' has been ended, please start a new Run or StreamingRun", proc.runID)
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

	registration, ok := proc.wf.ExecutorBindings[executorID]
	if !ok {
		return nil, fmt.Errorf("executor with ID '%s' is not registered", executorID)
	}

	executor, err := registration.CreateInstance(proc.runID)
	if err != nil {
		return nil, err
	}

	if err := executor.Initialize(proc.Bind(ctx, executorID, nil)); err != nil {
		return nil, err
	}

	if tracer != nil {
		tracer.TraceActivated(executorID)
	}

	// TODO: Handle special executor types (RequestInfoExecutor, WorkflowHostExecutor)

	proc.executors[executorID] = executor

	return executor, nil
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
			mapping.MapInto(proc.nextStep)
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
		if !proc.CompleteRequest(response.RequestID) {
			return fmt.Errorf("no pending request with ID %s found in the workflow context", response.RequestID)
		}

		mapping, err := proc.edgeMap.PrepareDeliveryForResponse(ctx, response)
		if err != nil {
			return err
		}

		if mapping != nil {
			mapping.MapInto(proc.nextStep)
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
	return proc.nextStep.HasMessages() || proc.HasQueuedExternalDeliveries() || proc.JoinedRunnersHaveActions()
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
	return (*execution.StepContext)(atomic.SwapPointer(
		(*unsafe.Pointer)(unsafe.Pointer(&proc.nextStep)),
		unsafe.Pointer(new(execution.StepContext))),
	), nil
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
	if err := proc.checkEnded(); err != nil {
		return err
	}

	// TODO: Add OpenTelemetry trace context propagation
	envelope, err := execution.NewMessageEnvelope(message, nil, sourceID, targetID)
	if err != nil {
		return err
	}
	edges := proc.wf.Edges[sourceID]
	for _, edge := range edges {
		mapping, err := proc.edgeMap.PrepareDeliveryForEdge(ctx, edge, envelope)
		if err != nil {
			return err
		}
		if mapping != nil {
			mapping.MapInto(proc.nextStep)
		}
	}

	return nil
}

// Bind creates a bound workflow context for a specific executor.
func (proc *runnerContext) Bind(ctx context.Context, executorID string, traceContext map[string]string) *workflow.Context {
	return &workflow.Context{
		Context: ctx,

		AddEvent: func(event workflow.Event) error {
			return proc.AddEvent(ctx, event)
		},

		SendMessage: func(targetID string, message any) error {
			return proc.SendMessage(ctx, executorID, targetID, message)
		},

		YieldOutput: func(output any) error {
			// Check if this executor can output
			if _, ok := proc.wf.OutputExecutors[executorID]; ok {
				return proc.AddEvent(ctx, workflow.OutputEvent{
					SourceID: executorID,
					Output:   output,
				})
			}
			return nil
		},

		RequestHalt: func() error {
			return proc.AddEvent(ctx, workflow.RequestHaltEvent{})
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
			value, err := proc.stateManager.ReadOrInitState(executorID, scope, key, func() any {
				// Call the init function to get the initial value
				if initFunc != nil {
					val, err := initFunc(ctx, key, scope)
					if err != nil {
						// Note: This error is swallowed by the factory pattern
						return nil
					}
					return val
				}
				return nil
			})
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

		ConcurrentRunsEnabled: func() bool {
			return proc.concurrentRunsEnabled
		},
	}
}

// Post posts an external request.
func (proc *runnerContext) Post(ctx context.Context, request *workflow.ExternalRequest) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}

	proc.requestsMu.Lock()
	if _, exists := proc.externalRequests[request.ID]; exists {
		proc.requestsMu.Unlock()
		return fmt.Errorf("pending request with id '%s' already exists", request.ID)
	}
	proc.externalRequests[request.ID] = request
	proc.requestsMu.Unlock()

	return proc.AddEvent(ctx, workflow.RequestInfoEvent{Request: request})
}

// CompleteRequest marks a request as completed.
func (proc *runnerContext) CompleteRequest(requestID string) bool {
	if err := proc.checkEnded(); err != nil {
		return false
	}

	proc.requestsMu.Lock()
	defer proc.requestsMu.Unlock()

	_, existed := proc.externalRequests[requestID]
	delete(proc.externalRequests, requestID)
	return existed
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

// WithCheckpointing returns whether checkpointing is enabled.
func (proc *runnerContext) WithCheckpointing() bool {
	return proc.withCheckpointing
}

// ConcurrentRunsEnabled returns whether concurrent runs are enabled.
func (proc *runnerContext) ConcurrentRunsEnabled() bool {
	return proc.concurrentRunsEnabled
}

// EndRun marks the run as ended and cleans up resources.
func (proc *runnerContext) EndRun(ctx context.Context) error {
	if proc.runEnded.Swap(true) {
		return nil // Already ended
	}
	if !proc.concurrentRunsEnabled {
		// Release workflow ownership
		if err := proc.wf.ReleaseOwnership(proc); err != nil {
			return err
		}
	}
	return nil
}
