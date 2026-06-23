// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/concurrent"
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

	edgeMap      *execution.EdgeRunner
	outputFilter *outputFilter
	nextStep     atomic.Pointer[execution.StepContext]

	executors concurrent.Map[string, *executorEntry]

	queuedExternalDeliveries concurrent.Queue[func(context.Context) error]

	joinedSubworkflowRunners concurrent.Map[string, execution.SuperStepRunner]

	externalRequests   concurrent.Map[string, *workflow.ExternalRequest]
	requestOwners      concurrent.Map[string, string] // requestID -> ownerExecutorID
	responsePortOwners concurrent.Map[string, string] // portID -> ownerExecutorID

	stateManager   execution.StateManager
	outgoingEvents execution.EventSink

	withCheckpointing     bool
	concurrentRunsEnabled bool
	previousOwnership     any
	runEnded              atomic.Bool
}

type executorEntry struct {
	ready    chan struct{}
	executor *workflow.Executor
	err      error
}

func newExecutorEntry() *executorEntry {
	return &executorEntry{ready: make(chan struct{})}
}

func (e *executorEntry) wait() (*workflow.Executor, error) {
	<-e.ready
	return e.executor, e.err
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
		wf:                    wf,
		sessionID:             sessionID,
		tracer:                tracer,
		outgoingEvents:        outgoingEvents,
		withCheckpointing:     withCheckpointing,
		concurrentRunsEnabled: enableConcurrentRuns,
		stateManager:          execution.NewStateManager(),
	}
	ctx.nextStep.Store(new(execution.StepContext))

	ctx.edgeMap = execution.NewEdgeRunner(wf, tracer, ctx.ensureExecutor)
	ctx.outputFilter = newOutputFilter(wf)
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

func (proc *runnerContext) executorSnapshot() ([]*workflow.Executor, error) {
	executors := make([]*workflow.Executor, 0)
	var snapshotErr error
	for _, entry := range proc.executors.All() {
		executor, err := entry.wait()
		if err != nil {
			snapshotErr = errors.Join(snapshotErr, err)
		} else if executor != nil {
			executors = append(executors, executor)
		}
	}
	return executors, snapshotErr
}

// ensureExecutor ensures an executor is instantiated and initialized.
func (proc *runnerContext) ensureExecutor(ctx context.Context, executorID string, tracer execution.StepTracer) (*workflow.Executor, error) {
	if err := proc.checkEnded(); err != nil {
		return nil, err
	}

	if entry, ok := proc.executors.Load(executorID); ok {
		return entry.wait()
	}
	entry := newExecutorEntry()
	actual, loaded := proc.executors.LoadOrStore(executorID, entry)
	if loaded {
		return actual.wait()
	}
	defer close(entry.ready)
	// Mirrors .NET's ConcurrentDictionary<string, Task<Executor>>: the stored
	// entry represents in-flight or completed initialization, so concurrent
	// callers wait on the same result instead of initializing twice.

	registration, ok := proc.wf.ExecutorBinding(executorID)
	if !ok {
		entry.err = fmt.Errorf("executor with ID '%s' is not registered", executorID)
		return nil, entry.err
	}

	executor, err := registration.CreateInstance(proc.sessionID)
	if err != nil {
		entry.err = err
		return nil, err
	}

	if err := executor.AttachRuntime(proc); err != nil {
		entry.err = err
		return nil, err
	}
	if err := executor.Initialize(proc.bind(ctx, executorID, nil)); err != nil {
		entry.err = err
		return nil, err
	}

	if tracer != nil {
		tracer.TraceInstantiated(executorID)
	}

	entry.executor = executor

	return executor, nil
}

func (proc *runnerContext) prepareForCheckpoint(ctx context.Context) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}

	executors, err := proc.executorSnapshot()
	if err != nil {
		return err
	}
	for _, executor := range executors {
		if err := executor.OnCheckpoint(proc.bind(ctx, executor.ID, nil)); err != nil {
			return err
		}
	}
	return nil
}

func (proc *runnerContext) notifyCheckpointLoaded(ctx context.Context) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}

	executors, err := proc.executorSnapshot()
	if err != nil {
		return err
	}
	for _, executor := range executors {
		if err := executor.OnCheckpointRestored(proc.bind(ctx, executor.ID, nil)); err != nil {
			return err
		}
	}
	return nil
}

func (proc *runnerContext) exportState() checkpoint.RunnerStateData {
	instantiated := make(map[string]struct{})
	for id := range proc.executors.All() {
		instantiated[id] = struct{}{}
	}

	outstanding := make([]*workflow.ExternalRequest, 0)
	for _, request := range proc.externalRequests.All() {
		outstanding = append(outstanding, request)
	}
	requestOwners := maps.Collect(proc.requestOwners.All())
	responsePortOwners := maps.Collect(proc.responsePortOwners.All())

	return checkpoint.RunnerStateData{
		InstantiatedExecutors: instantiated,
		QueuedMessages:        proc.currentStep().ExportMessages(),
		OutstandingRequests:   outstanding,
		RequestOwners:         requestOwners,
		ResponsePortOwners:    responsePortOwners,
	}
}

func (proc *runnerContext) importState(ctx context.Context, cp *checkpoint.Checkpoint) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	if cp == nil {
		return errors.New("checkpoint cannot be nil")
	}

	for executorID := range cp.RunnerData.InstantiatedExecutors {
		if _, ok := proc.executors.Load(executorID); !ok {
			if _, err := proc.ensureExecutor(ctx, executorID, nil); err != nil {
				return err
			}
		}
	}

	for {
		if _, ok := proc.queuedExternalDeliveries.Dequeue(); !ok {
			break
		}
	}

	nextStep := new(execution.StepContext)
	nextStep.ImportMessages(cp.RunnerData.QueuedMessages)
	proc.nextStep.Store(nextStep)

	proc.externalRequests.Clear()
	proc.requestOwners.Clear()
	proc.responsePortOwners.Clear()
	for requestID, ownerID := range cp.RunnerData.RequestOwners {
		proc.requestOwners.Store(requestID, ownerID)
	}
	for portID, ownerID := range cp.RunnerData.ResponsePortOwners {
		proc.responsePortOwners.Store(portID, ownerID)
	}
	for _, request := range cp.RunnerData.OutstandingRequests {
		proc.externalRequests.Store(request.RequestID, request)
		if ownerID, _ := proc.requestOwners.Load(request.RequestID); ownerID == "" {
			ownerID, _ = proc.responsePortOwners.Load(request.PortInfo.PortID)
			if ownerID == "" {
				ownerID = request.PortInfo.PortID
			}
			proc.requestOwners.Store(request.RequestID, ownerID)
		}
		if _, ok := proc.responsePortOwners.Load(request.PortInfo.PortID); !ok {
			ownerID, _ := proc.requestOwners.Load(request.RequestID)
			proc.responsePortOwners.Store(request.PortInfo.PortID, ownerID)
		}
	}

	return nil
}

// addExternalMessage queues an external message for delivery.
func (proc *runnerContext) addExternalMessage(message any, declaredType reflect.Type) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	if message == nil {
		return errors.New("message cannot be nil")
	}

	proc.queuedExternalDeliveries.Enqueue(func(ctx context.Context) error {
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

// addExternalResponse queues an external response for delivery.
func (proc *runnerContext) addExternalResponse(response *workflow.ExternalResponse) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	if response == nil {
		return errors.New("response cannot be nil")
	}

	proc.queuedExternalDeliveries.Enqueue(func(ctx context.Context) error {
		pending, existed := proc.externalRequests.Load(response.RequestID)
		if !existed {
			return fmt.Errorf("no pending request with ID %s found in the workflow context", response.RequestID)
		}

		// Reject responses whose PortInfo.PortID does not match the originating request's port to
		// prevent forged routing into unrelated port-specific execution paths.
		if pending != nil && pending.PortInfo.PortID != response.PortInfo.PortID {
			return fmt.Errorf("response port id %q does not match the originating port id for request %s", response.PortInfo.PortID, response.RequestID)
		}

		// Consume only after validation so a rejected response leaves the legitimate one able to complete.
		ownerID, existed := proc.completeRequest(response.RequestID)
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

// hasQueuedExternalDeliveries returns true if there are queued external deliveries.
func (proc *runnerContext) hasQueuedExternalDeliveries() bool {
	return !proc.queuedExternalDeliveries.IsEmpty()
}

// joinedRunnersHaveActions returns true if any joined subworkflow has actions.
func (proc *runnerContext) joinedRunnersHaveActions() bool {
	for runner := range proc.joinedSubworkflowRunners.Values() {
		if runner.HasUnprocessedMessages() {
			return true
		}
	}
	return false
}

// nextStepHasActions returns true if the next step has actions to process.
func (proc *runnerContext) nextStepHasActions() bool {
	return proc.currentStep().HasMessages() || proc.hasQueuedExternalDeliveries() || proc.joinedRunnersHaveActions()
}

// hasUnservicedRequests returns true if there are unserviced external requests.
func (proc *runnerContext) hasUnservicedRequests() bool {
	for range proc.externalRequests.All() {
		return true
	}

	for runner := range proc.joinedSubworkflowRunners.Values() {
		if runner.HasUnservicedRequests() {
			return true
		}
	}
	return false
}

// RepublishUnservicedRequests re-emits outstanding external requests after an
// event stream has subscribed, matching checkpoint-resume behavior.
func (proc *runnerContext) republishUnservicedRequests(ctx context.Context) error {
	requestIDs := make([]string, 0)
	requestsByID := make(map[string]*workflow.ExternalRequest)
	for requestID, request := range proc.externalRequests.All() {
		requestIDs = append(requestIDs, requestID)
		requestsByID[requestID] = request
	}
	slices.Sort(requestIDs)
	requests := make([]*workflow.ExternalRequest, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		requests = append(requests, requestsByID[requestID])
	}

	for _, request := range requests {
		if err := proc.addEvent(ctx, workflow.RequestInfoEvent{Request: request}); err != nil {
			return err
		}
	}
	return nil
}

// Advance advances to the next step, processing all queued external deliveries.
func (proc *runnerContext) advance(ctx context.Context) (*execution.StepContext, error) {
	if err := proc.checkEnded(); err != nil {
		return nil, err
	}

	for {
		delivery, ok := proc.queuedExternalDeliveries.Dequeue()
		if !ok {
			break
		}

		// Deliveries may mutate shared edge state, matching .NET's sequential TryDequeue loop.
		if err := delivery(ctx); err != nil {
			return nil, err
		}
	}

	// Swap out the next step
	return proc.nextStep.Swap(new(execution.StepContext)), nil
}

// AddEvent adds a workflow event to the outgoing event stream.
func (proc *runnerContext) addEvent(ctx context.Context, event workflow.Event) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	return proc.outgoingEvents.Enqueue(ctx, event)
}

// SendMessage sends a message from one executor to another through the workflow edges.
func (proc *runnerContext) sendMessage(ctx context.Context, sourceID, targetID string, message any) error {
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
	sourceExecutor, err := proc.ensureExecutor(ctx, sourceID, nil)
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
func (proc *runnerContext) bind(ctx context.Context, executorID string, traceContext map[string]string) *workflow.Context {
	boundCtx := ctx
	telemetry := observability.FromContext(ctx)
	boundCtx = observability.ContextWithTelemetry(boundCtx, telemetry)

	return &workflow.Context{
		Context: boundCtx,

		AddEvent: func(event workflow.Event) error {
			return proc.addEvent(boundCtx, event)
		},

		SendMessage: func(targetID string, message any) error {
			return proc.sendMessage(boundCtx, executorID, targetID, message)
		},

		YieldOutput: func(output any) error {
			return proc.yieldOutput(boundCtx, executorID, output)
		},

		RequestHalt: func() error {
			return proc.addEvent(boundCtx, workflow.RequestHaltEvent{})
		},

		PostRequest: func(request *workflow.ExternalRequest) error {
			return proc.post(boundCtx, executorID, request)
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

		QueueClearScope: func(scope string) error {
			return proc.stateManager.ClearState(executorID, scope)
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

func (proc *runnerContext) yieldOutput(ctx context.Context, executorID string, output any) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	if output == nil {
		return fmt.Errorf("executor %q cannot output nil", executorID)
	}

	sourceExecutor, err := proc.ensureExecutor(ctx, executorID, nil)
	if err != nil {
		return err
	}
	isAgentResponseShaped := isAgentResponseOutput(output)
	if !isAgentResponseShaped && !execution.CanOutputType(sourceExecutor, reflect.TypeOf(output)) {
		expectedTypes := make([]string, 0, len(sourceExecutor.DescribeProtocol().Yields))
		for _, typ := range sourceExecutor.DescribeProtocol().Yields {
			expectedTypes = append(expectedTypes, typ.String())
		}
		slices.Sort(expectedTypes)
		return fmt.Errorf("executor %q cannot output object of type %s; expected one of %v", executorID, reflect.TypeOf(output), expectedTypes)
	}

	tags, ok := proc.outputFilter.tryGetTags(executorID)
	if !ok {
		return nil
	}
	return proc.addEvent(ctx, workflow.OutputEvent{
		ExecutorID: executorID,
		Output:     output,
		Tags:       tags,
	})
}

func isAgentResponseOutput(output any) bool {
	switch output.(type) {
	case *agent.Response, agent.Response, *agent.ResponseUpdate, agent.ResponseUpdate:
		return true
	default:
		return false
	}
}

// Post raises an external request originating from the executor identified
// by ownerID. The matching response (delivered later via [AddExternalResponse])
// will be routed back to that executor as a regular message.
func (proc *runnerContext) post(ctx context.Context, ownerID string, request *workflow.ExternalRequest) error {
	if err := proc.checkEnded(); err != nil {
		return err
	}
	if ownerID == "" {
		return errors.New("ownerID is required when posting an external request")
	}
	if request == nil {
		return errors.New("request cannot be nil")
	}

	if _, exists := proc.externalRequests.LoadOrStore(request.RequestID, request); exists {
		return fmt.Errorf("pending request with id '%s' already exists", request.RequestID)
	}
	proc.requestOwners.Store(request.RequestID, ownerID)
	proc.responsePortOwners.Store(request.PortInfo.PortID, ownerID)

	return proc.addEvent(ctx, workflow.RequestInfoEvent{Request: request})
}

// completeRequest marks a request as completed and returns the executor that
// posted it.
func (proc *runnerContext) completeRequest(requestID string) (string, bool) {
	if err := proc.checkEnded(); err != nil {
		return "", false
	}

	_, existed := proc.externalRequests.LoadAndDelete(requestID)
	owner, _ := proc.requestOwners.LoadAndDelete(requestID)
	return owner, existed
}

// ResponsePortExecutorID returns the executor that handles responses on the
// given port, or ("", false) if no such port is registered. Mirrors .NET's
// [EdgeMap.TryGetResponsePortExecutorId].
func (proc *runnerContext) responsePortExecutorID(portID string) (string, bool) {
	return proc.responsePortOwners.Load(portID)
}

// joinedSubworkflowRunnerSnapshot returns all joined subworkflow runners.
func (proc *runnerContext) joinedSubworkflowRunnerSnapshot() []execution.SuperStepRunner {
	runners := make([]execution.SuperStepRunner, 0)
	for runner := range proc.joinedSubworkflowRunners.Values() {
		runners = append(runners, runner)
	}
	return runners
}

// attachSuperstep attaches a subworkflow runner.
func (proc *runnerContext) attachSuperstep(runner execution.SuperStepRunner) (string, error) {
	for {
		joinID := strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, exists := proc.joinedSubworkflowRunners.LoadOrStore(joinID, runner); !exists {
			return joinID, nil
		}
	}
}

// detachSuperstep detaches a subworkflow runner.
func (proc *runnerContext) detachSuperstep(joinID string) bool {
	_, existed := proc.joinedSubworkflowRunners.LoadAndDelete(joinID)
	return existed
}

// endRun marks the run as ended and cleans up resources.
func (proc *runnerContext) endRun(ctx context.Context) error {
	if proc.runEnded.Swap(true) {
		return nil // Already ended
	}
	var runErr error

	executors, err := proc.executorSnapshot()
	if err != nil {
		runErr = errors.Join(runErr, err)
	}
	for _, executor := range executors {
		if err := executor.Close(ctx); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}

	joinedRunners := slices.Collect(proc.joinedSubworkflowRunners.Values())
	proc.joinedSubworkflowRunners.Clear()

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
