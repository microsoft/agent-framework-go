// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

var (
	// Default is the default execution environment.
	Default = OffThread

	// OffThread is an environment which will run steps in a background goroutine, streaming events out as they are raised.
	OffThread = newExecutionEnvironment(execution.ModeOffThread, false)

	// Concurrent is like [OffThread], but enables concurrent execution.
	Concurrent = newExecutionEnvironment(execution.ModeOffThread, true)

	// Lockstep is an environment which will run steps in the event watching tread,
	// accumulating events during each step and streaming them out after each step is completed.
	Lockstep = newExecutionEnvironment(execution.ModeLockstep, false)

	// Subworkflow is an environment which will not run steps directly, relying instead
	// on the hosting workflow to run them directly, while streaming events out as they are raised.
	Subworkflow = newExecutionEnvironment(execution.ModeSubworkflow, false)
)

func NewInMemoryCheckpointManager() checkpoint.Manager {
	return checkpoint.NewInMemoryManager()
}

// ExecutionEnvironment provides an in-process workflow execution environment
// for running, streaming, and checkpointing workflows.
type ExecutionEnvironment struct {
	executionMode        execution.Mode
	enableConcurrentRuns bool
	checkpointManager    checkpoint.Manager
}

func newExecutionEnvironment(mode execution.Mode, enableConcurrentRuns bool, checkpointManager ...checkpoint.Manager) *ExecutionEnvironment {
	var cm checkpoint.Manager
	if len(checkpointManager) > 0 {
		cm = checkpointManager[0]
	}
	return &ExecutionEnvironment{
		executionMode:        mode,
		enableConcurrentRuns: enableConcurrentRuns,
		checkpointManager:    cm,
	}
}

// WithCheckpointing returns a new execution environment with the same
// execution settings and the provided checkpoint manager.
func (e *ExecutionEnvironment) WithCheckpointing(cm checkpoint.Manager) *ExecutionEnvironment {
	return newExecutionEnvironment(e.executionMode, e.enableConcurrentRuns, cm)
}

// IsCheckpointingEnabled reports whether checkpointing is configured for
// this environment.
func (e *ExecutionEnvironment) IsCheckpointingEnabled() bool {
	return e.checkpointManager != nil
}

func (e *ExecutionEnvironment) RunStreaming(ctx context.Context, wf *workflow.Workflow, msg any, opts ...ExecutionOption) (*StreamingRun, error) {
	options := applyExecutionOptions(opts)
	handle, err := e.beginRun(ctx, wf, options.SessionID, nil)
	if err != nil {
		return nil, err
	}
	if msg != nil {
		if err := handle.EnqueueMessage(ctx, msg); err != nil {
			return nil, err
		}
	}
	return newStreamingRun(handle), nil
}

func (e *ExecutionEnvironment) ResumeStreaming(ctx context.Context, wf *workflow.Workflow, fromCheckpoint workflow.CheckpointInfo, opts ...ExecutionOption) (*StreamingRun, error) {
	if err := e.verifyCheckpointingConfigured(); err != nil {
		return nil, err
	}
	handle, err := e.resumeRun(ctx, wf, fromCheckpoint, nil, opts...)
	if err != nil {
		return nil, err
	}
	return newStreamingRun(handle), nil
}

func (e *ExecutionEnvironment) Run(ctx context.Context, wf *workflow.Workflow, msg any, opts ...ExecutionOption) (*Run, error) {
	options := applyExecutionOptions(opts)
	handle, err := e.beginRunHandlingChatProtocol(ctx, wf, options.SessionID, msg)
	if err != nil {
		return nil, err
	}
	run := newRun(handle)
	if _, err := run.RunToNextHalt(ctx); err != nil {
		return nil, err
	}
	return run, nil
}

func (e *ExecutionEnvironment) Resume(ctx context.Context, wf *workflow.Workflow, fromCheckpoint workflow.CheckpointInfo, opts ...ExecutionOption) (*Run, error) {
	if err := e.verifyCheckpointingConfigured(); err != nil {
		return nil, err
	}
	handle, err := e.resumeRun(ctx, wf, fromCheckpoint, nil, opts...)
	if err != nil {
		return nil, err
	}
	run := newRun(handle)
	if _, err := run.RunToNextHalt(ctx); err != nil {
		return nil, err
	}
	return run, nil
}

func (e *ExecutionEnvironment) verifyCheckpointingConfigured() error {
	if e.checkpointManager == nil {
		return errors.New("checkpointing is not configured for this execution environment; use WithCheckpointing to attach a checkpoint manager")
	}
	return nil
}

func (e *ExecutionEnvironment) beginRun(ctx context.Context, wf *workflow.Workflow, sessionID string, knownValidInputTypes []reflect.Type) (*execution.RunHandle, error) {
	return e.beginRunWithCheckpointManager(ctx, wf, e.checkpointManager, sessionID, knownValidInputTypes)
}

func (e *ExecutionEnvironment) beginRunWithCheckpointManager(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, sessionID string, knownValidInputTypes []reflect.Type) (*execution.RunHandle, error) {
	runner, err := createTopLevelRunner(wf, cm, sessionID, e.enableConcurrentRuns, knownValidInputTypes)
	if err != nil {
		return nil, err
	}
	return runner.beginStream(ctx, e.executionMode)
}

func (e *ExecutionEnvironment) resumeRun(ctx context.Context, wf *workflow.Workflow, fromCheckpoint workflow.CheckpointInfo, knownValidInputTypes []reflect.Type, opts ...ExecutionOption) (*execution.RunHandle, error) {
	return e.resumeRunWithCheckpointManager(ctx, wf, e.checkpointManager, fromCheckpoint, knownValidInputTypes, opts...)
}

func (e *ExecutionEnvironment) resumeRunWithCheckpointManager(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, ch workflow.CheckpointInfo, knownValidInputTypes []reflect.Type, opts ...ExecutionOption) (*execution.RunHandle, error) {
	options := applyExecutionOptions(opts)
	sessionID := ch.SessionID
	if options.SessionID != "" {
		sessionID = options.SessionID
	}
	return e.resumeRunWithCheckpointManagerRepublish(ctx, wf, cm, sessionID, ch, knownValidInputTypes, !options.DisablePendingRequestRepublish)
}

func (e *ExecutionEnvironment) resumeRunWithCheckpointManagerRepublish(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, sessionID string, ch workflow.CheckpointInfo, knownValidInputTypes []reflect.Type, republishPendingRequests bool) (*execution.RunHandle, error) {
	runner, err := createTopLevelRunner(wf, cm, sessionID, e.enableConcurrentRuns, knownValidInputTypes)
	if err != nil {
		return nil, err
	}
	return runner.resumeStreamWithRepublish(ctx, e.executionMode, ch, republishPendingRequests)
}

func (e *ExecutionEnvironment) beginRunHandlingChatProtocol(ctx context.Context, wf *workflow.Workflow, sessionID string, msg any) (*execution.RunHandle, error) {
	return e.beginRunHandlingChatProtocolWithCheckpointManager(ctx, wf, e.checkpointManager, sessionID, msg)
}

func (e *ExecutionEnvironment) beginRunHandlingChatProtocolWithCheckpointManager(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, sessionID string, msg any) (*execution.RunHandle, error) {
	descriptor, err := wf.DescribeProtocol()
	if err != nil {
		return nil, err
	}
	handle, err := e.beginRunWithCheckpointManager(ctx, wf, cm, sessionID, descriptor.Accepts)
	if err != nil {
		return nil, err
	}
	var hasToken bool
	if msg != nil {
		if err := handle.EnqueueMessage(ctx, msg); err != nil {
			return nil, err
		}
		if !hasToken && reflect.TypeOf(msg) == reflect.TypeFor[workflow.TurnToken]() {
			hasToken = true
		}
	}
	if !hasToken && slices.Contains(descriptor.Accepts, reflect.TypeFor[workflow.TurnToken]()) {
		emitEvents := true
		if err := handle.EnqueueMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
			return nil, err
		}
	}

	return handle, nil
}
