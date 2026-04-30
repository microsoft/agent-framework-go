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

var _ workflow.ExecutionEnvironment = (*ExecutionEnvironment)(nil)

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

func OpenStream(ctx context.Context, wf *workflow.Workflow, sessionID string) (workflow.StreamingRun, error) {
	return Default.RunStreaming(ctx, wf, sessionID)
}

func Stream(ctx context.Context, wf *workflow.Workflow, sessionID string, msgs ...any) (workflow.StreamingRun, error) {
	return Default.RunStreaming(ctx, wf, sessionID, msgs...)
}

func StreamWithCheckpoint(ctx context.Context, wf *workflow.Workflow, sessionID string, cm checkpoint.Manager, msgs ...any) (*workflow.Checkpointed[workflow.StreamingRun], error) {
	return Default.StreamWithCheckpoint(ctx, wf, sessionID, cm, msgs...)
}

func Run(ctx context.Context, wf *workflow.Workflow, sessionID string, msgs ...any) (workflow.Run, error) {
	return Default.Run(ctx, wf, sessionID, msgs...)
}

func RunWithCheckpoint(ctx context.Context, wf *workflow.Workflow, sessionID string, cm checkpoint.Manager, msgs ...any) (*workflow.Checkpointed[workflow.Run], error) {
	return Default.RunWithCheckpoint(ctx, wf, sessionID, cm, msgs...)
}

func Resume(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, sessionID string, ch workflow.CheckpointInfo) (*workflow.Checkpointed[workflow.Run], error) {
	return Default.ResumeWithCheckpoint(ctx, wf, cm, sessionID, ch)
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

func (e *ExecutionEnvironment) RunStreaming(ctx context.Context, wf *workflow.Workflow, sessionID string, msgs ...any) (workflow.StreamingRun, error) {
	handle, err := e.beginRun(ctx, wf, sessionID, nil)
	if err != nil {
		return nil, err
	}
	for _, msg := range msgs {
		if err := handle.EnqueueMessage(ctx, msg); err != nil {
			return nil, err
		}
	}
	return execution.NewStreamingRun(handle), nil
}

func (e *ExecutionEnvironment) StreamWithCheckpoint(ctx context.Context, wf *workflow.Workflow, sessionID string, cm checkpoint.Manager, msgs ...any) (*workflow.Checkpointed[workflow.StreamingRun], error) {
	handle, err := e.beginRunWithCheckpointManager(ctx, wf, cm, sessionID, nil)
	if err != nil {
		return nil, err
	}
	for _, msg := range msgs {
		if err := handle.EnqueueMessage(ctx, msg); err != nil {
			return nil, err
		}
	}
	return workflow.NewCheckpointed[workflow.StreamingRun](execution.NewStreamingRun(handle), handle), nil
}

func (e *ExecutionEnvironment) ResumeStreaming(ctx context.Context, wf *workflow.Workflow, fromCheckpoint workflow.CheckpointInfo) (workflow.StreamingRun, error) {
	if err := e.verifyCheckpointingConfigured(); err != nil {
		return nil, err
	}
	handle, err := e.resumeRun(ctx, wf, fromCheckpoint, nil)
	if err != nil {
		return nil, err
	}
	return execution.NewStreamingRun(handle), nil
}

func (e *ExecutionEnvironment) Run(ctx context.Context, wf *workflow.Workflow, sessionID string, msgs ...any) (workflow.Run, error) {
	handle, err := e.beginRunHandlingChatProtocol(ctx, wf, sessionID, msgs...)
	if err != nil {
		return nil, err
	}
	run := execution.NewRun(handle)
	if _, err := run.RunToNextHalt(ctx); err != nil {
		return nil, err
	}
	return run, nil
}

func (e *ExecutionEnvironment) RunWithCheckpoint(ctx context.Context, wf *workflow.Workflow, sessionID string, cm checkpoint.Manager, msgs ...any) (*workflow.Checkpointed[workflow.Run], error) {
	handle, err := e.beginRunHandlingChatProtocolWithCheckpointManager(ctx, wf, cm, sessionID, msgs...)
	if err != nil {
		return nil, err
	}
	run := execution.NewRun(handle)
	if _, err := run.RunToNextHalt(ctx); err != nil {
		return nil, err
	}
	return workflow.NewCheckpointed[workflow.Run](run, handle), nil
}

func (e *ExecutionEnvironment) Resume(ctx context.Context, wf *workflow.Workflow, fromCheckpoint workflow.CheckpointInfo) (workflow.Run, error) {
	if err := e.verifyCheckpointingConfigured(); err != nil {
		return nil, err
	}
	handle, err := e.resumeRun(ctx, wf, fromCheckpoint, nil)
	if err != nil {
		return nil, err
	}
	run := execution.NewRun(handle)
	if _, err := run.RunToNextHalt(ctx); err != nil {
		return nil, err
	}
	return run, nil
}

func (e *ExecutionEnvironment) ResumeWithCheckpoint(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, sessionID string, ch workflow.CheckpointInfo) (*workflow.Checkpointed[workflow.Run], error) {
	handle, err := e.resumeRunWithCheckpointManager(ctx, wf, cm, sessionID, ch, nil)
	if err != nil {
		return nil, err
	}
	return workflow.NewCheckpointed[workflow.Run](execution.NewRun(handle), handle), nil
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

func (e *ExecutionEnvironment) resumeRun(ctx context.Context, wf *workflow.Workflow, fromCheckpoint workflow.CheckpointInfo, knownValidInputTypes []reflect.Type) (*execution.RunHandle, error) {
	return e.resumeRunWithCheckpointManager(ctx, wf, e.checkpointManager, fromCheckpoint.SessionID, fromCheckpoint, knownValidInputTypes)
}

func (e *ExecutionEnvironment) resumeRunWithCheckpointManager(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, sessionID string, ch workflow.CheckpointInfo, knownValidInputTypes []reflect.Type) (*execution.RunHandle, error) {
	runner, err := createTopLevelRunner(wf, cm, sessionID, e.enableConcurrentRuns, knownValidInputTypes)
	if err != nil {
		return nil, err
	}
	return runner.resumeStream(ctx, e.executionMode, ch)
}

func (e *ExecutionEnvironment) beginRunHandlingChatProtocol(ctx context.Context, wf *workflow.Workflow, sessionID string, msgs ...any) (*execution.RunHandle, error) {
	return e.beginRunHandlingChatProtocolWithCheckpointManager(ctx, wf, e.checkpointManager, sessionID, msgs...)
}

func (e *ExecutionEnvironment) beginRunHandlingChatProtocolWithCheckpointManager(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, sessionID string, msgs ...any) (*execution.RunHandle, error) {
	descriptor, err := wf.DescribeProtocol()
	if err != nil {
		return nil, err
	}
	handle, err := e.beginRunWithCheckpointManager(ctx, wf, cm, sessionID, descriptor.Accepts)
	if err != nil {
		return nil, err
	}
	var hasToken bool
	for _, msg := range msgs {
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
