// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

// Run represents a non-streaming, in-process workflow run. It advances the
// workflow to the next halt and accumulates the events raised so far so they
// can be replayed via OutgoingEvents or consumed incrementally via NewEvents.
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

// SessionID returns the unique identifier for this run's session.
func (run *Run) SessionID() string {
	return run.runHandle.SessionID()
}

// IsCheckpointingEnabled reports whether a checkpoint manager is configured for this run.
func (run *Run) IsCheckpointingEnabled() bool {
	return run.runHandle.IsCheckpointingEnabled()
}

// Checkpoints returns the list of created checkpoints.
func (run *Run) Checkpoints() []workflow.CheckpointInfo {
	return run.runHandle.Checkpoints()
}

// LastCheckpoint returns the most recently created checkpoint, or false if no
// checkpoint has been created yet.
func (run *Run) LastCheckpoint() (workflow.CheckpointInfo, bool) {
	return run.runHandle.LastCheckpoint()
}

// RestoreCheckpoint restores the workflow state from the given checkpoint.
func (run *Run) RestoreCheckpoint(ctx context.Context, checkpointInfo workflow.CheckpointInfo) error {
	return run.runHandle.RestoreCheckpoint(ctx, checkpointInfo)
}

// GetStatus returns the current execution status of the run.
func (run *Run) GetStatus(ctx context.Context) (RunStatus, error) {
	return run.runHandle.GetStatus(ctx)
}

// OutgoingEvents returns an iterator over all events accumulated by the run so far.
func (run *Run) OutgoingEvents() iter.Seq[workflow.Event] {
	return slices.Values(run.eventSink)
}

// NewEventCount returns the number of accumulated events not yet consumed by NewEvents.
func (run *Run) NewEventCount() int {
	return len(run.eventSink) - run.lastBookmark
}

// NewEvents returns an iterator over the events accumulated since the previous
// call to NewEvents. The internal bookmark is advanced to the current end of the
// event sink as soon as iteration begins, so every event returned here is
// consumed even if the caller stops iterating early; a subsequent call will only
// yield events accumulated after this call.
func (run *Run) NewEvents() iter.Seq[workflow.Event] {
	if run.lastBookmark >= len(run.eventSink) {
		return func(yield func(workflow.Event) bool) {}
	}
	return func(yield func(workflow.Event) bool) {
		current := run.lastBookmark
		run.lastBookmark = len(run.eventSink)
		for _, evt := range run.eventSink[current:] {
			if !yield(evt) {
				return
			}
		}
	}
}

// Resume enqueues the given messages and advances the workflow to the next
// halt, returning whether any events were raised.
func (run *Run) Resume(ctx context.Context, messages ...any) (bool, error) {
	for _, msg := range messages {
		if err := run.runHandle.EnqueueMessage(ctx, msg); err != nil {
			return false, err
		}
	}
	return run.RunToNextHalt(ctx)
}

// Close ends the run and releases its resources.
func (run *Run) Close(ctx context.Context) error {
	return run.runHandle.Close(ctx)
}

// RunToNextHalt advances the workflow until it halts, appending any raised
// events to the run and returning whether any events were raised.
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

// StreamingRun represents a streaming, in-process workflow run. Events are
// delivered to the caller as they are raised rather than accumulated, and new
// messages or responses can be sent to the workflow while it is running.
type StreamingRun struct {
	runHandle *execution.RunHandle
}

func newStreamingRun(handle *execution.RunHandle) *StreamingRun {
	return &StreamingRun{
		runHandle: handle,
	}
}

// SessionID returns the unique identifier for this run's session.
func (stream *StreamingRun) SessionID() string {
	return stream.runHandle.SessionID()
}

// IsCheckpointingEnabled reports whether a checkpoint manager is configured for this run.
func (stream *StreamingRun) IsCheckpointingEnabled() bool {
	return stream.runHandle.IsCheckpointingEnabled()
}

// Checkpoints returns the list of created checkpoints.
func (stream *StreamingRun) Checkpoints() []workflow.CheckpointInfo {
	return stream.runHandle.Checkpoints()
}

// LastCheckpoint returns the most recently created checkpoint, or false if no
// checkpoint has been created yet.
func (stream *StreamingRun) LastCheckpoint() (workflow.CheckpointInfo, bool) {
	return stream.runHandle.LastCheckpoint()
}

// RestoreCheckpoint restores the workflow state from the given checkpoint.
func (stream *StreamingRun) RestoreCheckpoint(ctx context.Context, checkpointInfo workflow.CheckpointInfo) error {
	return stream.runHandle.RestoreCheckpoint(ctx, checkpointInfo)
}

// GetStatus returns the current execution status of the run.
func (stream *StreamingRun) GetStatus(ctx context.Context) (RunStatus, error) {
	return stream.runHandle.GetStatus(ctx)
}

// SendResponse delivers an external response to a pending request in the workflow.
func (stream *StreamingRun) SendResponse(ctx context.Context, response *workflow.ExternalResponse) error {
	return stream.runHandle.EnqueueResponse(ctx, response)
}

// SendMessage enqueues a message as input to the workflow.
func (stream *StreamingRun) SendMessage(ctx context.Context, message any) error {
	return stream.runHandle.EnqueueMessage(ctx, message)
}

// ResponsePortExecutorID returns the executor that handles responses on the
// given port, or ("", false) if no such port is registered.
func (stream *StreamingRun) ResponsePortExecutorID(portID string) (string, bool) {
	return stream.runHandle.ResponsePortExecutorID(portID)
}

// WatchStream returns an iterator over the workflow's events, blocking on
// pending requests so the stream stays open until they are serviced. Only one
// consumer may watch the stream at a time.
func (stream *StreamingRun) WatchStream(ctx context.Context) iter.Seq2[workflow.Event, error] {
	return stream.runHandle.TakeEventStream(ctx, true)
}

// WatchUntilHalt returns an iterator over the workflow's events that completes
// once the workflow halts, without blocking on pending requests. Only one
// consumer may watch the stream at a time.
func (stream *StreamingRun) WatchUntilHalt(ctx context.Context) iter.Seq2[workflow.Event, error] {
	return stream.runHandle.TakeEventStream(ctx, false)
}

// CancelRun cancels the run, stopping event delivery.
func (stream *StreamingRun) CancelRun() error {
	stream.runHandle.Cancel()
	return nil
}

// Close ends the run and releases its resources.
func (stream *StreamingRun) Close(ctx context.Context) error {
	return stream.runHandle.Close(ctx)
}

// RunStatus describes the current execution state of a [Run] or [StreamingRun].
type RunStatus = execution.RunStatus

const (
	RunStatusNotStarted      = execution.RunStatusNotStarted
	RunStatusIdle            = execution.RunStatusIdle
	RunStatusPendingRequests = execution.RunStatusPendingRequests
	RunStatusEnded           = execution.RunStatusEnded
	RunStatusRunning         = execution.RunStatusRunning
)

// ExecutionOption configures an individual run started via the execution
// environment's Run, RunStreaming, Resume, or ResumeStreaming methods.
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
