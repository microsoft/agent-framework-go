// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/microsoft/agent-framework/go/internal/concurrent"
	"github.com/microsoft/agent-framework/go/workflow"
)

type RunEventStream interface {
	Start()
	SignalInput()
	Stop()

	GetStatus(ctx context.Context) (workflow.RunStatus, error)

	TakeEventStream(ctx context.Context, blockOnPendingRequest bool) iter.Seq2[workflow.Event, error]
}

type inputWaiter struct {
	signal chan struct{}
	closed atomic.Bool
}

func newInputWaiter() inputWaiter {
	return inputWaiter{
		// Buffered channel of size 1 → acts like a binary semaphore
		signal: make(chan struct{}, 1),
	}
}

// signalInput: non-blocking signal (swallow if already signaled)
func (iw *inputWaiter) signalInput() {
	if iw.closed.Load() {
		return
	}
	select {
	case iw.signal <- struct{}{}:
		// Successfully signaled
	default:
		// Already signaled, swallow (binary semaphore behavior)
	}
}

// waitForInput: waits for signal or timeout/cancellation
func (iw *inputWaiter) waitForInput(ctx context.Context, timeout time.Duration) error {
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	select {
	case <-iw.signal:
		return nil // Got signal
	case <-ctx.Done():
		return ctx.Err() // Timeout or cancellation
	}
}

// close closes the inputWaiter and releases any waiting goroutines
func (iw *inputWaiter) close() {
	if iw.closed.CompareAndSwap(false, true) {
		close(iw.signal)
	}
}

var _ RunEventStream = (*streamingRunEventStream)(nil)

// streamingRunEventStream is a modern implementation of RunEventStream that streams events as they are created,
// using channels for thread-safe coordination.
type streamingRunEventStream struct {
	eventChannel    chan workflow.Event
	stepRunner      SuperStepRunner
	inputWaiter     inputWaiter
	runLoopCancel   context.CancelFunc
	runLoopCtx      context.Context
	disableRunLoop  bool
	runLoopDone     chan struct{}
	runStatus       atomic.Int32 // stores RunStatus
	completionEpoch atomic.Int64
}

func newStreamingRunEventStream(stepRunner SuperStepRunner, disableRunLoop bool) *streamingRunEventStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &streamingRunEventStream{
		stepRunner:     stepRunner,
		inputWaiter:    newInputWaiter(),
		runLoopCancel:  cancel,
		runLoopCtx:     ctx,
		disableRunLoop: disableRunLoop,
		runLoopDone:    make(chan struct{}),
		// Unbounded channel - events never block the producer
		// This allows events to flow freely during superstep execution
		eventChannel: make(chan workflow.Event),
	}
}

func (s *streamingRunEventStream) Start() {
	// Start the background run loop that drives superstep execution
	if !s.disableRunLoop {
		go s.runLoop()
	}
}

func (s *streamingRunEventStream) runLoop() {
	defer close(s.runLoopDone)
	defer close(s.eventChannel)
	defer s.setStatus(workflow.RunStatusEnded)

	ctx, cancel := context.WithCancel(s.runLoopCtx)
	defer cancel()

	// Subscribe to events - they will flow directly to the channel as they're raised
	s.stepRunner.OutgoingEvents().EventRaised = append(
		s.stepRunner.OutgoingEvents().EventRaised,
		s.onEventRaised,
	)

	// Wait for the first input before starting
	// The consumer will call EnqueueMessage which signals the run loop
	if err := s.inputWaiter.waitForInput(ctx, 0); err != nil {
		return
	}

	s.setStatus(workflow.RunStatusRunning)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Run all available supersteps continuously
		// Events are streamed out in real-time as they happen via the event handler
		for s.stepRunner.HasUnprocessedMessages() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if _, err := s.stepRunner.RunSuperStep(ctx); err != nil {
				// Send error event
				s.sendEvent(ctx, workflow.ErrorEvent{Error: err})
				cancel()
				return
			}
		}

		// Update status based on what's waiting
		if s.stepRunner.HasUnservicedRequests() {
			s.setStatus(workflow.RunStatusPendingRequests)
		} else {
			s.setStatus(workflow.RunStatusIdle)
		}

		// Signal completion to consumer so they can check status and decide whether to continue
		// Increment epoch so next consumer iteration gets a new completion signal
		currentEpoch := s.completionEpoch.Add(1)
		capturedStatus := s.getStatus()

		// Send internal halt signal
		select {
		case s.eventChannel <- &internalHaltSignal{epoch: currentEpoch, status: capturedStatus}:
		case <-ctx.Done():
			return
		}

		// Wait for next input from the consumer
		// Works for both Idle (no work) and PendingRequests (waiting for responses)
		if err := s.inputWaiter.waitForInput(ctx, time.Second); err != nil {
			if ctx.Err() != nil {
				return
			}
			// Timeout - continue to next iteration
		}

		// When signaled, resume running
		s.setStatus(workflow.RunStatusRunning)
	}
}

func (s *streamingRunEventStream) onEventRaised(ctx context.Context, sender any, evt workflow.Event) error {
	// Write event directly to channel - non-blocking with buffered channel
	select {
	case s.eventChannel <- evt:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.runLoopCtx.Done():
		return s.runLoopCtx.Err()
	}
}

func (s *streamingRunEventStream) sendEvent(ctx context.Context, evt workflow.Event) {
	select {
	case s.eventChannel <- evt:
	case <-ctx.Done():
	case <-s.runLoopCtx.Done():
	}
}

// SignalInput signals that new input has been provided and the run loop should continue processing.
// Called by RunHandle when the user enqueues a message or response.
func (s *streamingRunEventStream) SignalInput() {
	s.inputWaiter.signalInput()
}

func (s *streamingRunEventStream) TakeEventStream(ctx context.Context, blockOnPendingRequest bool) iter.Seq2[workflow.Event, error] {
	// Get the current epoch - we'll only respond to completion signals from this epoch or later
	myEpoch := s.completionEpoch.Load() + 1

	return func(yield func(workflow.Event, error) bool) {
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-s.eventChannel:
				if !ok {
					// Channel closed
					return
				}

				// Filter out internal signals used for run loop coordination
				if signal, ok := evt.(*internalHaltSignal); ok {
					// Ignore completion signals from previous iterations
					if signal.epoch < myEpoch {
						continue
					}

					// Check if we should stop streaming based on the status captured at completion time
					// - Idle: Workflow completed, no pending requests
					// - Ended: Run loop cancelled
					if signal.status == workflow.RunStatusIdle || signal.status == workflow.RunStatusEnded {
						return
					}

					if !blockOnPendingRequest && signal.status == workflow.RunStatusPendingRequests {
						return
					}

					// Otherwise continue reading (more events coming after input provided)
					continue
				}

				// Send event to consumer
				if !yield(evt, nil) {
					return
				}
			}
		}
	}
}

func (s *streamingRunEventStream) GetStatus(ctx context.Context) (workflow.RunStatus, error) {
	return s.getStatus(), nil
}

func (s *streamingRunEventStream) getStatus() workflow.RunStatus {
	return workflow.RunStatus(s.runStatus.Load())
}

func (s *streamingRunEventStream) setStatus(status workflow.RunStatus) {
	s.runStatus.Store(int32(status))
}

// ClearBufferedEvents clears all buffered events from the channel.
// This should be called when restoring a checkpoint to discard stale events from superseded supersteps.
func (s *streamingRunEventStream) ClearBufferedEvents() {
	// Drain all events currently in the channel buffer
	for {
		select {
		case <-s.eventChannel:
			// Discard each event
		default:
			// Channel empty
			s.SignalInput()
			return
		}
	}
}

func (s *streamingRunEventStream) Stop() {
	// Cancel the run loop
	s.runLoopCancel()

	// Release the event waiter, if any
	s.inputWaiter.signalInput()

	// Wait for clean shutdown
	<-s.runLoopDone

	// Close the input waiter
	s.inputWaiter.close()
}

// internalHaltSignal is used to mark completion of a work batch and allow status checking.
// This is never exposed to consumers.
type internalHaltSignal struct {
	epoch  int64
	status workflow.RunStatus
}

func (s *internalHaltSignal) Data() any {
	return s
}

var _ RunEventStream = (*lockstepRunEventStream)(nil)

// lockstepRunEventStream is a synchronous implementation of RunEventStream that collects events
// during superstep execution and yields them after each step completes.
type lockstepRunEventStream struct {
	stepRunner  SuperStepRunner
	inputWaiter inputWaiter
	stopCtx     context.Context
	stopCancel  context.CancelFunc
	runStatus   atomic.Int32 // stores RunStatus
}

func newLockstepRunEventStream(stepRunner SuperStepRunner) *lockstepRunEventStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &lockstepRunEventStream{
		stepRunner:  stepRunner,
		inputWaiter: newInputWaiter(),
		stopCtx:     ctx,
		stopCancel:  cancel,
	}
}

func (l *lockstepRunEventStream) Start() {
	// No-op for lockstep execution
}

func (l *lockstepRunEventStream) GetStatus(ctx context.Context) (workflow.RunStatus, error) {
	return l.getStatus(), nil
}

func (l *lockstepRunEventStream) getStatus() workflow.RunStatus {
	return workflow.RunStatus(l.runStatus.Load())
}

func (l *lockstepRunEventStream) setStatus(status workflow.RunStatus) {
	l.runStatus.Store(int32(status))
}

func (l *lockstepRunEventStream) TakeEventStream(ctx context.Context, blockOnPendingRequest bool) iter.Seq2[workflow.Event, error] {
	return func(yield func(workflow.Event, error) bool) {
		linkedCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Combine stop context with caller's context
		go func() {
			select {
			case <-l.stopCtx.Done():
				cancel()
			case <-linkedCtx.Done():
			}
		}()

		// Event collection queue
		var eventSink concurrent.Queue[workflow.Event]

		// Subscribe to events
		eventHandler := func(ctx context.Context, sender any, evt workflow.Event) error {
			eventSink.Enqueue(evt)
			return nil
		}

		l.stepRunner.OutgoingEvents().EventRaised = append(
			l.stepRunner.OutgoingEvents().EventRaised,
			eventHandler,
		)
		defer func() {
			// Update status
			if l.stepRunner.HasUnservicedRequests() {
				l.setStatus(workflow.RunStatusPendingRequests)
			} else {
				l.setStatus(workflow.RunStatusIdle)
			}
			// Remove event handler
			handlers := l.stepRunner.OutgoingEvents().EventRaised
			for i, h := range handlers {
				if fmt.Sprintf("%p", h) == fmt.Sprintf("%p", eventHandler) {
					l.stepRunner.OutgoingEvents().EventRaised = append(handlers[:i], handlers[i+1:]...)
					break
				}
			}
		}()

		l.setStatus(workflow.RunStatusRunning)

		for {
			// Run all available supersteps
			for l.stepRunner.HasUnprocessedMessages() && linkedCtx.Err() == nil {
				if _, err := l.stepRunner.RunSuperStep(linkedCtx); err != nil {
					if !errors.Is(err, context.Canceled) {
						yield(nil, err)
					}
					return
				}

				// Collect and send events
				events := (*concurrent.Queue[workflow.Event])(atomic.SwapPointer(
					(*unsafe.Pointer)(unsafe.Pointer(&eventSink)),
					unsafe.Pointer(new(concurrent.Queue[workflow.Event]))),
				)

				var hadRequestHaltEvent bool
				for evt := range events.All() {
					if errors.Is(linkedCtx.Err(), context.Canceled) {
						return // Exit if cancellation is requested
					}
					if _, ok := evt.(workflow.RequestHaltEvent); ok {
						hadRequestHaltEvent = true
						continue
					}
					if !yield(evt, nil) {
						return
					}
				}

				if hadRequestHaltEvent || linkedCtx.Err() != nil {
					return
				}
			}

			// Update status
			if l.stepRunner.HasUnservicedRequests() {
				l.setStatus(workflow.RunStatusPendingRequests)
			} else {
				l.setStatus(workflow.RunStatusIdle)
			}

			// Check if we should break
			status := l.getStatus()
			if l.shouldBreak(status, blockOnPendingRequest, linkedCtx) {
				return
			}

			// If blocking on pending requests and we have pending requests, wait for input
			if blockOnPendingRequest && status == workflow.RunStatusPendingRequests {
				if err := l.inputWaiter.waitForInput(linkedCtx, time.Second); err != nil {
					if linkedCtx.Err() != nil {
						return
					}
					// Timeout - continue
				}
			} else {
				// No more work to do
				return
			}
		}
	}
}

func (l *lockstepRunEventStream) shouldBreak(status workflow.RunStatus, blockOnPendingRequest bool, ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	if status == workflow.RunStatusIdle || status == workflow.RunStatusEnded {
		return true
	}
	if status == workflow.RunStatusPendingRequests && !blockOnPendingRequest {
		return true
	}
	return false
}

// SignalInput signals that new input has been provided and the run loop should continue processing.
func (l *lockstepRunEventStream) SignalInput() {
	l.inputWaiter.signalInput()
}

func (l *lockstepRunEventStream) Stop() {
	l.stopCancel()
	// Close the input waiter
	l.inputWaiter.close()
}
