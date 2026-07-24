// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

type Run struct {
	runHandle *execution.RunHandle
	eventSink []workflow.Event

	lastBookmark int
}

func newRun(handle *execution.RunHandle) *Run {
	return &Run{
		runHandle: handle,
	}
}

func (run *Run) SessionID() string {
	return run.runHandle.SessionID()
}

func (run *Run) IsCheckpointingEnabled() bool {
	return run.runHandle.IsCheckpointingEnabled()
}

func (run *Run) Checkpoints() []workflow.CheckpointInfo {
	return run.runHandle.Checkpoints()
}

func (run *Run) LastCheckpoint() (workflow.CheckpointInfo, bool) {
	return run.runHandle.LastCheckpoint()
}

func (run *Run) RestoreCheckpoint(ctx context.Context, checkpointInfo workflow.CheckpointInfo) error {
	return run.runHandle.RestoreCheckpoint(ctx, checkpointInfo)
}

func (run *Run) GetStatus(ctx context.Context) (RunStatus, error) {
	return run.runHandle.GetStatus(ctx)
}

func (run *Run) OutgoingEvents() iter.Seq[workflow.Event] {
	return slices.Values(run.eventSink)
}

func (run *Run) NewEventCount() int {
	return len(run.eventSink) - run.lastBookmark
}

func (run *Run) NewEvents() iter.Seq[workflow.Event] {
	return func(yield func(workflow.Event) bool) {
		// Advance the read bookmark as each event is delivered so that stopping
		// the iteration early leaves the un-yielded events available to a later
		// NewEvents call instead of silently discarding them.
		for run.lastBookmark < len(run.eventSink) {
			evt := run.eventSink[run.lastBookmark]
			run.lastBookmark++
			if !yield(evt) {
				return
			}
		}
	}
}

func (run *Run) Resume(ctx context.Context, messages ...any) (bool, error) {
	for _, msg := range messages {
		if err := run.runHandle.EnqueueMessage(ctx, msg); err != nil {
			return false, err
		}
	}
	return run.RunToNextHalt(ctx)
}

func (run *Run) Close(ctx context.Context) error {
	return run.runHandle.Close(ctx)
}

func (run *Run) RunToNextHalt(ctx context.Context) (bool, error) {
	var hadEvents bool
	for evt, err := range run.runHandle.TakeEventStream(ctx, false) {
		if err != nil {
			return false, err
		}
		hadEvents = true
		run.eventSink = append(run.eventSink, evt)
	}
	return hadEvents, nil
}

type StreamingRun struct {
	runHandle *execution.RunHandle
}

func newStreamingRun(handle *execution.RunHandle) *StreamingRun {
	return &StreamingRun{
		runHandle: handle,
	}
}

func (stream *StreamingRun) SessionID() string {
	return stream.runHandle.SessionID()
}

func (stream *StreamingRun) IsCheckpointingEnabled() bool {
	return stream.runHandle.IsCheckpointingEnabled()
}

func (stream *StreamingRun) Checkpoints() []workflow.CheckpointInfo {
	return stream.runHandle.Checkpoints()
}

func (stream *StreamingRun) LastCheckpoint() (workflow.CheckpointInfo, bool) {
	return stream.runHandle.LastCheckpoint()
}

func (stream *StreamingRun) RestoreCheckpoint(ctx context.Context, checkpointInfo workflow.CheckpointInfo) error {
	return stream.runHandle.RestoreCheckpoint(ctx, checkpointInfo)
}

func (stream *StreamingRun) GetStatus(ctx context.Context) (RunStatus, error) {
	return stream.runHandle.GetStatus(ctx)
}

func (stream *StreamingRun) SendResponse(ctx context.Context, response *workflow.ExternalResponse) error {
	return stream.runHandle.EnqueueResponse(ctx, response)
}

func (stream *StreamingRun) SendMessage(ctx context.Context, message any) error {
	return stream.runHandle.EnqueueMessage(ctx, message)
}

func (stream *StreamingRun) ResponsePortExecutorID(portID string) (string, bool) {
	return stream.runHandle.ResponsePortExecutorID(portID)
}

func (stream *StreamingRun) WatchStream(ctx context.Context) iter.Seq2[workflow.Event, error] {
	return stream.runHandle.TakeEventStream(ctx, true)
}

func (stream *StreamingRun) WatchUntilHalt(ctx context.Context) iter.Seq2[workflow.Event, error] {
	return stream.runHandle.TakeEventStream(ctx, false)
}

func (stream *StreamingRun) CancelRun() error {
	stream.runHandle.Cancel()
	return nil
}

func (stream *StreamingRun) Close(ctx context.Context) error {
	return stream.runHandle.Close(ctx)
}

type RunStatus = execution.RunStatus

const (
	RunStatusNotStarted      = execution.RunStatusNotStarted
	RunStatusIdle            = execution.RunStatusIdle
	RunStatusPendingRequests = execution.RunStatusPendingRequests
	RunStatusEnded           = execution.RunStatusEnded
	RunStatusRunning         = execution.RunStatusRunning
)

type ExecutionOption func(*executionOptions)

type executionOptions struct {
	SessionID                      string
	DisablePendingRequestRepublish bool
}

// WithSessionID sets the workflow session ID for a new run. It is honored by
// Run and RunStreaming. Resume and ResumeStreaming use the session ID from
// the checkpoint being resumed.
func WithSessionID(sessionID string) ExecutionOption {
	return func(options *executionOptions) {
		options.SessionID = sessionID
	}
}

// WithPendingRequestRepublish controls whether outstanding external requests
// are re-emitted as RequestInfoEvent values when resuming from a checkpoint.
// It defaults to true. It is accepted by Resume and ResumeStreaming.
func WithPendingRequestRepublish(enabled bool) ExecutionOption {
	return func(options *executionOptions) {
		options.DisablePendingRequestRepublish = !enabled
	}
}

func applyExecutionOptions(opts []ExecutionOption) executionOptions {
	var options executionOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}
