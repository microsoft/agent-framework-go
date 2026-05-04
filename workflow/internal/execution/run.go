// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"sync/atomic"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

type DeliveryMapping struct {
	Envelopes []*MessageEnvelope
	Targets   []*workflow.Executor
}

func (d DeliveryMapping) MapInto(nextStep *StepContext) {
	for _, target := range d.Targets {
		messageQueue := nextStep.MessagesFor(target.ID)
		for _, env := range d.Envelopes {
			messageQueue.Enqueue(env)
		}
	}
}

var _ checkpoint.CheckpointingHandle = (*RunHandle)(nil)

type RunHandle struct {
	stepRunner       SuperStepRunner
	checkpointHandle checkpoint.CheckpointingHandle
	eventStream      RunEventStream

	endRunCtx          context.Context
	endRunCancel       context.CancelFunc
	closed             atomic.Bool
	isEventStreamTaken atomic.Bool
}

func NewRunHandle(sr SuperStepRunner, ch checkpoint.CheckpointingHandle, mode Mode) *RunHandle {
	if sr == nil {
		panic("SuperStepRunner cannot be nil")
	}
	if ch == nil {
		panic("CheckpointingHandle cannot be nil")
	}

	var eventStream RunEventStream
	switch mode {
	case ModeOffThread:
		eventStream = newStreamingRunEventStream(sr, false)
	case ModeLockstep:
		eventStream = newLockstepRunEventStream(sr)
	case ModeSubworkflow:
		eventStream = newStreamingRunEventStream(sr, true)
	default:
		panic(fmt.Errorf("invalid execution mode %d", mode))
	}

	ctx, cancel := context.WithCancel(context.Background())

	handle := &RunHandle{
		stepRunner:       sr,
		checkpointHandle: ch,
		eventStream:      eventStream,
		endRunCtx:        ctx,
		endRunCancel:     cancel,
	}

	eventStream.Start()

	// If there are already unprocessed messages or unserviced requests (e.g.,
	// from a checkpoint restore that happened before this handle was created),
	// signal the run loop to start processing them.
	if sr.HasUnprocessedMessages() || sr.HasUnservicedRequests() {
		handle.signalInputToRunLoop()
	}

	return handle
}

func (h *RunHandle) SessionID() string {
	return h.stepRunner.SessionID()
}

func (h *RunHandle) IsCheckpointingEnabled() bool {
	return h.checkpointHandle.IsCheckpointingEnabled()
}

func (h *RunHandle) Checkpoints() []workflow.CheckpointInfo {
	return h.checkpointHandle.Checkpoints()
}

func (h *RunHandle) LastCheckpoint() (workflow.CheckpointInfo, bool) {
	checkpoints := h.Checkpoints()
	if len(checkpoints) == 0 {
		return workflow.CheckpointInfo{}, false
	}
	return checkpoints[len(checkpoints)-1], true
}

func (h *RunHandle) GetStatus(ctx context.Context) (RunStatus, error) {
	return h.eventStream.GetStatus(ctx)
}

// TakeEventStream returns a channel of workflow events. Only one consumer can take the event stream at a time.
// If blockOnPendingRequest is true, the stream will wait for responses to pending requests before completing.
func (h *RunHandle) TakeEventStream(ctx context.Context, blockOnPendingRequest bool) iter.Seq2[workflow.Event, error] {
	return func(yield func(workflow.Event, error) bool) {
		// Enforce single active enumerator
		if !h.isEventStreamTaken.CompareAndSwap(false, true) {
			yield(nil, fmt.Errorf("the event stream has already been taken. Only one consumer is allowed at a time"))
			return
		}
		defer func() {
			h.isEventStreamTaken.Store(false)
		}()

		// Create linked context with end run cancellation
		linkedCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-h.endRunCtx.Done():
				cancel()
			case <-linkedCtx.Done():
			}
		}()

		for evt, err := range h.eventStream.TakeEventStream(linkedCtx, blockOnPendingRequest) {
			if err != nil {
				yield(nil, err)
				return
			}
			if linkedCtx.Err() != nil {
				yield(nil, linkedCtx.Err())
				return
			}
			if _, ok := evt.(workflow.RequestHaltEvent); ok {
				// Filter out the RequestHaltEvent, since it is an internal signalling event.
				return
			}
			if !yield(evt, nil) {
				break
			}
		}
	}
}

func (h *RunHandle) IsValidInputType(ctx context.Context, typ reflect.Type) bool {
	return h.stepRunner.IsValidInputType(ctx, typ)
}

func (h *RunHandle) EnqueueMessage(ctx context.Context, message any) error {
	if response, ok := message.(*workflow.ExternalResponse); ok {
		return h.EnqueueResponse(ctx, response)
	}
	if message == nil {
		return fmt.Errorf("message cannot be nil")
	}
	if !h.IsValidInputType(ctx, reflect.TypeOf(message)) {
		return fmt.Errorf("message type %v is not a valid input type for this workflow: %w", reflect.TypeOf(message), workflow.ErrInvalidInputType)
	}
	if err := h.stepRunner.EnqueueMessage(ctx, message); err != nil {
		return err
	}

	// Signal the run loop that new input is available
	h.signalInputToRunLoop()

	return nil
}

func (h *RunHandle) EnqueueResponse(ctx context.Context, response *workflow.ExternalResponse) error {
	if err := h.stepRunner.EnqueueResponse(ctx, response); err != nil {
		return err
	}

	// Signal the run loop that new input is available
	h.signalInputToRunLoop()

	return nil
}

func (h *RunHandle) ResponsePortExecutorID(portID string) (string, bool) {
	return h.stepRunner.ResponsePortExecutorID(portID)
}

func (h *RunHandle) signalInputToRunLoop() {
	h.eventStream.SignalInput()
}

func (h *RunHandle) Cancel() {
	h.endRunCancel()
	h.eventStream.Stop()
}

func (h *RunHandle) Close(ctx context.Context) error {
	if h.closed.CompareAndSwap(false, true) {
		// Cancel the run if it is still running
		h.Cancel()

		// Request end of run
		if err := h.stepRunner.RequestEndRun(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (h *RunHandle) RestoreCheckpoint(ctx context.Context, checkpointInfo workflow.CheckpointInfo) error {
	// Clear buffered events from the channel BEFORE restoring to discard stale events from supersteps
	// that occurred after the checkpoint we're restoring to
	// This must happen BEFORE the restore so that events republished during restore aren't cleared
	if bufferedEventStream, ok := h.eventStream.(interface{ ClearBufferedEvents() }); ok {
		bufferedEventStream.ClearBufferedEvents()
	}

	// Restore the workflow state - this will republish unserviced requests as new events
	if err := h.checkpointHandle.RestoreCheckpoint(ctx, checkpointInfo); err != nil {
		return err
	}

	// After restore, signal the run loop to process any restored messages
	// This is necessary because ClearBufferedEvents() doesn't signal, and the restored
	// queued messages won't automatically wake up the run loop
	h.signalInputToRunLoop()

	return nil
}
