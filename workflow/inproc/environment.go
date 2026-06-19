// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	internalcheckpoint "github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

var (
	// Default is the default execution environment.
	Default = OffThread

	// OffThread is an environment which will run steps in a background goroutine, streaming events out as they are raised.
	OffThread = newExecutionEnvironment(execution.ModeOffThread, false, nil)

	// Concurrent is like [OffThread], but enables concurrent execution.
	Concurrent = newExecutionEnvironment(execution.ModeOffThread, true, nil)

	// Lockstep is an environment which will run steps in the event watching tread,
	// accumulating events during each step and streaming them out after each step is completed.
	Lockstep = newExecutionEnvironment(execution.ModeLockstep, false, nil)
)

// ExecutionEnvironment provides an in-process workflow execution environment
// for running, streaming, and checkpointing workflows.
type ExecutionEnvironment struct {
	executionMode        execution.Mode
	enableConcurrentRuns bool
	checkpointManager    internalcheckpoint.Manager
}

func newExecutionEnvironment(mode execution.Mode, enableConcurrentRuns bool, checkpointManager internalcheckpoint.Manager) *ExecutionEnvironment {
	return &ExecutionEnvironment{
		executionMode:        mode,
		enableConcurrentRuns: enableConcurrentRuns,
		checkpointManager:    checkpointManager,
	}
}

// WithCheckpointing returns a new execution environment configured with
// the given [checkpoint.Manager].
func (e *ExecutionEnvironment) WithCheckpointing(mgr checkpoint.Manager) *ExecutionEnvironment {
	if mgr == nil {
		return newExecutionEnvironment(e.executionMode, e.enableConcurrentRuns, nil)
	}
	return newExecutionEnvironment(e.executionMode, e.enableConcurrentRuns, mgr.(internalcheckpoint.Manager))
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
	runner, err := createTopLevelRunner(wf, e.checkpointManager, sessionID, e.enableConcurrentRuns, knownValidInputTypes)
	if err != nil {
		return nil, err
	}
	return runner.beginStream(ctx, e.executionMode)
}

func (e *ExecutionEnvironment) resumeRun(ctx context.Context, wf *workflow.Workflow, fromCheckpoint workflow.CheckpointInfo, knownValidInputTypes []reflect.Type, opts ...ExecutionOption) (*execution.RunHandle, error) {
	options := applyExecutionOptions(opts)
	runner, err := createTopLevelRunner(wf, e.checkpointManager, fromCheckpoint.SessionID, e.enableConcurrentRuns, knownValidInputTypes)
	if err != nil {
		return nil, err
	}
	return runner.resumeStreamWithRepublish(ctx, e.executionMode, fromCheckpoint, !options.DisablePendingRequestRepublish)
}

func (e *ExecutionEnvironment) beginRunHandlingChatProtocol(ctx context.Context, wf *workflow.Workflow, sessionID string, msg any) (*execution.RunHandle, error) {
	descriptor, err := wf.DescribeProtocol()
	if err != nil {
		return nil, err
	}
	handle, err := e.beginRun(ctx, wf, sessionID, descriptor.Accepts)
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
