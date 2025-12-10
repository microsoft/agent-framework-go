// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework/go/internal/concurrent"
	"github.com/microsoft/agent-framework/go/internal/errgroup"
	"github.com/microsoft/agent-framework/go/workflow"
	"github.com/microsoft/agent-framework/go/workflow/internal/checkpoint"
	"github.com/microsoft/agent-framework/go/workflow/internal/execution"
)

var _ execution.SuperStepRunner = (*runner)(nil)
var _ checkpoint.CheckpointingHandle = (*runner)(nil)

// runner provides a local, in-process runner for executing a workflow.
// It enables step-by-step execution of a workflow graph entirely within the current process,
// without distributed coordination. It is primarily intended for testing, debugging, or
// scenarios where workflow execution does not require executor distribution.
type runner struct {
	runID           string
	startExecutorID string
	wf              *workflow.Workflow
	runContext      *runnerContext
	checkpointMgr   checkpoint.Manager
	edgeMap         *execution.EdgeRunner
	stepTracer      *stepTracer
	outgoingEvents  *execution.ConcurrentEventSink

	knownValidInputTypes map[reflect.Type]struct{}

	workflowInfoCache *checkpoint.WorkflowInfo
	checkpoints       []workflow.CheckpointInfo
}

// createTopLevelRunner creates a new top-level InProcessRunner for the given workflow.
func createTopLevelRunner(
	wf *workflow.Workflow,
	checkpointMgr checkpoint.Manager,
	runID string,
	enableConcurrentRuns bool,
	knownValidInputTypes []reflect.Type,
) (*runner, error) {
	if runID == "" {
		runID = uuid.NewString()
	}
	return newInProcessRunner(wf, checkpointMgr, runID, nil, enableConcurrentRuns, knownValidInputTypes)
}

// createSubworkflowRunner creates a new subworkflow InProcessRunner.
func createSubworkflowRunner(
	wf *workflow.Workflow,
	checkpointMgr checkpoint.Manager,
	runID string,
	existingOwnerSignoff any,
	enableConcurrentRuns bool,
	knownValidInputTypes []reflect.Type,
) (*runner, error) {
	if runID == "" {
		runID = uuid.NewString()
	}
	return newInProcessRunner(wf, checkpointMgr, runID, existingOwnerSignoff, enableConcurrentRuns, knownValidInputTypes)
}

func newInProcessRunner(
	wf *workflow.Workflow,
	checkpointMgr checkpoint.Manager,
	runID string,
	existingOwnerSignoff any,
	enableConcurrentRuns bool,
	knownValidInputTypes []reflect.Type,
) (*runner, error) {
	if wf == nil {
		return nil, fmt.Errorf("workflow cannot be nil")
	}

	if enableConcurrentRuns {
		var nonConcurrent []string
		for _, er := range wf.ExecutorBindings {
			if !er.SupportsConcurrentSharedExecution {
				nonConcurrent = append(nonConcurrent, er.ID)
			}
		}
		if len(nonConcurrent) > 0 {
			slices.Sort(nonConcurrent)
		}
		return nil, fmt.Errorf("workflow must only consist of cross-run share-capable or factory-created executors. Executors not supporting concurrent: %v", nonConcurrent)
	}

	stepTracer := new(stepTracer)
	outgoingEvents := &execution.ConcurrentEventSink{}

	runContext, err := newInProcessRunnerContext(
		wf,
		runID,
		checkpointMgr != nil,
		outgoingEvents,
		stepTracer,
		existingOwnerSignoff,
		enableConcurrentRuns,
	)
	if err != nil {
		return nil, err
	}

	edgeMap := execution.NewEdgeRunner(wf, stepTracer, runContext.EnsureExecutor)

	runner := &runner{
		runID:                runID,
		startExecutorID:      wf.StartExecutorID,
		wf:                   wf,
		runContext:           runContext,
		checkpointMgr:        checkpointMgr,
		edgeMap:              edgeMap,
		stepTracer:           stepTracer,
		outgoingEvents:       outgoingEvents,
		knownValidInputTypes: make(map[reflect.Type]struct{}),
	}

	// Initialize known valid input types
	for _, typ := range knownValidInputTypes {
		runner.knownValidInputTypes[typ] = struct{}{}
	}

	return runner, nil
}

// RunID returns the unique identifier for this run.
func (r *runner) RunID() string {
	return r.runID
}

// StartExecutorID returns the ID of the starting executor.
func (r *runner) StartExecutorID() string {
	return r.startExecutorID
}

// OutgoingEvents returns the event sink for outgoing workflow events.
func (r *runner) OutgoingEvents() *execution.ConcurrentEventSink {
	return r.outgoingEvents
}

// HasUnservicedRequests returns true if there are unserviced external requests.
func (r *runner) HasUnservicedRequests() bool {
	return r.runContext.HasUnservicedRequests()
}

// HasUnprocessedMessages returns true if the next step has actions to process.
func (r *runner) HasUnprocessedMessages() bool {
	return r.runContext.NextStepHasActions()
}

// IsValidInputType checks if the given type is a valid input type for this workflow.
func (r *runner) IsValidInputType(ctx context.Context, messageType reflect.Type) bool {
	if _, known := r.knownValidInputTypes[messageType]; known {
		return true
	}

	startingExecutor, err := r.runContext.EnsureExecutor(ctx, r.startExecutorID, nil)
	if err != nil {
		return false
	}

	if startingExecutor.CanHandleType(messageType) {
		r.knownValidInputTypes[messageType] = struct{}{}
		return true
	}

	return false
}

func (r *runner) beginStream(_ context.Context, mode execution.Mode) (*execution.RunHandle, error) {
	if err := r.runContext.checkEnded(); err != nil {
		return nil, err
	}
	return execution.NewRunHandle(r, r, mode), nil
}

func (r *runner) resumeStream(ctx context.Context, mode execution.Mode, info workflow.CheckpointInfo) (*execution.RunHandle, error) {
	if err := r.runContext.checkEnded(); err != nil {
		return nil, err
	}
	if r.checkpointMgr == nil {
		return nil, fmt.Errorf("this runner was not configured with a CheckpointManager, so it cannot resume from checkpoints")
	}
	if err := r.RestoreCheckpoint(ctx, info); err != nil {
		return nil, err
	}
	return execution.NewRunHandle(r, r, mode), nil
}

// EnqueueMessage enqueues a typed message to the workflow.
func (r *runner) EnqueueMessage(ctx context.Context, message any) error {
	if err := r.runContext.checkEnded(); err != nil {
		return err
	}
	if message == nil {
		return fmt.Errorf("message cannot be nil")
	}

	if response, ok := message.(*workflow.ExternalResponse); ok {
		return r.runContext.AddExternalResponse(ctx, response)
	}

	messageType := reflect.TypeOf(message)
	if !r.IsValidInputType(ctx, messageType) {
		return fmt.Errorf("message type %v is not a valid input type for this workflow", messageType)
	}

	return r.runContext.AddExternalMessage(ctx, message, messageType)
}

// EnqueueResponse enqueues an external response to the workflow.
func (r *runner) EnqueueResponse(ctx context.Context, response *workflow.ExternalResponse) error {
	return r.runContext.AddExternalResponse(ctx, response)
}

// RunSuperStep executes a single super step of the workflow.
func (r *runner) RunSuperStep(ctx context.Context) (bool, error) {
	if err := r.runContext.checkEnded(); err != nil {
		return false, err
	}

	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	currentStep, err := r.runContext.Advance(ctx)
	if err != nil {
		return false, err
	}

	if currentStep.HasMessages() ||
		r.runContext.HasQueuedExternalDeliveries() ||
		r.runContext.JoinedRunnersHaveActions() {

		if err := r.runSuperstep(ctx, currentStep); err != nil {
			if !errors.Is(err, context.Canceled) {
				r.outgoingEvents.Enqueue(ctx, workflow.ErrorEvent{Error: err})
			}
		}
		return true, nil
	}
	return false, nil
}

// RequestEndRun requests the workflow run to end.
func (r *runner) RequestEndRun(ctx context.Context) error {
	return r.runContext.EndRun(ctx)
}

// Checkpoints returns the list of created checkpoints.
func (r *runner) Checkpoints() []workflow.CheckpointInfo {
	return r.checkpoints
}

// RestoreCheckpoint restores the workflow state from a checkpoint.
func (r *runner) RestoreCheckpoint(ctx context.Context, checkpointInfo workflow.CheckpointInfo) error {
	if err := r.runContext.checkEnded(); err != nil {
		return err
	}

	if r.checkpointMgr == nil {
		return fmt.Errorf("this runner was not configured with a CheckpointManager, so it cannot restore checkpoints")
	}

	cp, err := r.checkpointMgr.Lookup(r.runID)
	if err != nil {
		return fmt.Errorf("failed to lookup checkpoint: %w", err)
	}

	// Validate the checkpoint is compatible with this workflow
	if !cp.WorkflowInfo.Match(r.wf) {
		return fmt.Errorf("the specified checkpoint is not compatible with the workflow associated with this runner")
	}

	if err := r.runContext.stateManager.ImportState(cp); err != nil {
		return err
	}
	// TODO: Implement full checkpoint restoration
	return fmt.Errorf("checkpoint restore not yet implemented")
}

func (r *runner) runSuperstep(ctx context.Context, currentStep *execution.StepContext) error {
	// Raise superstep started event
	evt := r.stepTracer.Advance(currentStep)
	if err := r.outgoingEvents.Enqueue(ctx, evt); err != nil {
		return err
	}

	// Deliver messages to receivers concurrently
	g, gctx := errgroup.WithContext(ctx)
	for _, receiverID := range currentStep.Keys() {
		g.Go(func() error {
			return r.deliverMessages(gctx, receiverID, currentStep.MessagesFor(receiverID))
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// Process subworkflows
	for _, subRunner := range r.runContext.JoinedSubworkflowRunners() {
		if _, err := subRunner.RunSuperStep(ctx); err != nil {
			return err
		}
	}

	// Create checkpoint
	if err := r.checkpoint(ctx); err != nil {
		return err
	}

	// Raise superstep completed event
	completeEvt := r.stepTracer.Complete(r.runContext.NextStepHasActions(), r.runContext.HasUnservicedRequests())
	return r.outgoingEvents.Enqueue(ctx, completeEvt)
}

func (r *runner) deliverMessages(ctx context.Context, receiverID string, envelopes *concurrent.Queue[*execution.MessageEnvelope]) error {
	executor, err := r.runContext.EnsureExecutor(ctx, receiverID, r.stepTracer)
	if err != nil {
		return err
	}

	r.stepTracer.TraceActivated(receiverID)

	for {
		envelope, ok := envelopes.Dequeue()
		if !ok {
			break
		}
		boundCtx := r.runContext.Bind(ctx, receiverID, envelope.TraceContext)
		if _, err := executor.Execute(boundCtx, envelope.Message); err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) checkpoint(ctx context.Context) error {
	if err := r.runContext.checkEnded(); err != nil {
		return err
	}

	if r.checkpointMgr == nil {
		// Always publish state updates, even without a CheckpointManager
		return r.runContext.stateManager.PublishUpdates(r.stepTracer)
	}

	// TODO: Implement full checkpoint creation
	// This requires implementing WorkflowInfo and state export for all components

	return r.runContext.stateManager.PublishUpdates(r.stepTracer)
}
