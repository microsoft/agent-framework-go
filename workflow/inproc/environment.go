// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework/go/workflow"
	"github.com/microsoft/agent-framework/go/workflow/internal/checkpoint"
	"github.com/microsoft/agent-framework/go/workflow/internal/execution"
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

func OpenStream(ctx context.Context, wf *workflow.Workflow, runID string) (workflow.StreamingRun, error) {
	return Default.OpenStream(ctx, wf, runID)
}

func Stream(ctx context.Context, wf *workflow.Workflow, runID string, msgs ...any) (workflow.StreamingRun, error) {
	return Default.Stream(ctx, wf, runID, msgs...)
}

func StreamWithCheckpoint(ctx context.Context, wf *workflow.Workflow, runID string, cm checkpoint.Manager, msgs ...any) (*workflow.Checkpointed[workflow.StreamingRun], error) {
	return Default.StreamWithCheckpoint(ctx, wf, runID, cm, msgs...)
}

func Run(ctx context.Context, wf *workflow.Workflow, runID string, msgs ...any) (workflow.Run, error) {
	return Default.Run(ctx, wf, runID, msgs...)
}

func RunWithCheckpoint(ctx context.Context, wf *workflow.Workflow, runID string, cm checkpoint.Manager, msgs ...any) (*workflow.Checkpointed[workflow.Run], error) {
	return Default.RunWithCheckpoint(ctx, wf, runID, cm, msgs...)
}

func Resume(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, runID string, ch workflow.CheckpointInfo) (*workflow.Checkpointed[workflow.Run], error) {
	return Default.Resume(ctx, wf, cm, runID, ch)
}

type ExecutionEnvironment struct {
	mode                 execution.Mode
	enableConcurrentRuns bool
}

func newExecutionEnvironment(mode execution.Mode, enableConcurrentRuns bool) *ExecutionEnvironment {
	return &ExecutionEnvironment{
		mode:                 mode,
		enableConcurrentRuns: enableConcurrentRuns,
	}
}

func (e *ExecutionEnvironment) OpenStream(ctx context.Context, wf *workflow.Workflow, runID string) (workflow.StreamingRun, error) {
	handle, err := e.beginStreamRun(ctx, wf, nil, runID, nil)
	if err != nil {
		return nil, err
	}
	return execution.NewStreamingRun(handle), nil
}

func (e *ExecutionEnvironment) Stream(ctx context.Context, wf *workflow.Workflow, runID string, msgs ...any) (workflow.StreamingRun, error) {
	handle, err := e.beginStreamRun(ctx, wf, nil, runID, nil)
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

func (e *ExecutionEnvironment) StreamWithCheckpoint(ctx context.Context, wf *workflow.Workflow, runID string, cm checkpoint.Manager, msgs ...any) (*workflow.Checkpointed[workflow.StreamingRun], error) {
	handle, err := e.resumeStreamRun(ctx, wf, cm, runID, workflow.CheckpointInfo{}, nil)
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

func (e *ExecutionEnvironment) Run(ctx context.Context, wf *workflow.Workflow, runID string, msgs ...any) (workflow.Run, error) {
	handle, err := e.beginRun(ctx, wf, nil, runID, msgs...)
	if err != nil {
		return nil, err
	}
	run := execution.NewRun(handle)
	if _, err := run.RunToNextHalt(ctx); err != nil {
		return nil, err
	}
	return run, nil
}

func (e *ExecutionEnvironment) RunWithCheckpoint(ctx context.Context, wf *workflow.Workflow, runID string, cm checkpoint.Manager, msgs ...any) (*workflow.Checkpointed[workflow.Run], error) {
	handle, err := e.beginRun(ctx, wf, cm, runID, msgs...)
	if err != nil {
		return nil, err
	}
	run := execution.NewRun(handle)
	if _, err := run.RunToNextHalt(ctx); err != nil {
		return nil, err
	}
	return workflow.NewCheckpointed[workflow.Run](run, handle), nil
}

func (e *ExecutionEnvironment) Resume(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, runID string, ch workflow.CheckpointInfo) (*workflow.Checkpointed[workflow.Run], error) {
	handle, err := e.resumeStreamRun(ctx, wf, cm, runID, ch, nil)
	if err != nil {
		return nil, err
	}
	return workflow.NewCheckpointed[workflow.Run](execution.NewRun(handle), handle), nil
}

func (e *ExecutionEnvironment) beginStreamRun(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, runID string, knownValidInputTypes []reflect.Type) (*execution.RunHandle, error) {
	runner, err := createTopLevelRunner(wf, cm, runID, e.enableConcurrentRuns, knownValidInputTypes)
	if err != nil {
		return nil, err
	}
	return runner.beginStream(ctx, e.mode)
}

func (e *ExecutionEnvironment) resumeStreamRun(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, runID string, ch workflow.CheckpointInfo, knownValidInputTypes []reflect.Type) (*execution.RunHandle, error) {
	runner, err := createTopLevelRunner(wf, cm, runID, e.enableConcurrentRuns, knownValidInputTypes)
	if err != nil {
		return nil, err
	}
	return runner.resumeStream(ctx, e.mode, ch)
}

func (e *ExecutionEnvironment) beginRun(ctx context.Context, wf *workflow.Workflow, cm checkpoint.Manager, runID string, msgs ...any) (*execution.RunHandle, error) {
	descriptor, err := wf.DescribeProtocol()
	if err != nil {
		return nil, err
	}
	handle, err := e.beginStreamRun(ctx, wf, cm, runID, descriptor.Accepts)
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
