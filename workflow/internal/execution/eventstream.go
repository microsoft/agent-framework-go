// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"

	"github.com/microsoft/agent-framework-go/internal/concurrent"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/observability"
)

type RunEventStream interface {
	Start()
	SignalInput()
	Stop()

	GetStatus(ctx context.Context) (RunStatus, error)

	TakeEventStream(ctx context.Context, blockOnPendingRequest bool) iter.Seq2[workflow.Event, error]
}

func workflowMetadata(wf *workflow.Workflow, sessionID string) observability.WorkflowMetadata {
	if wf == nil {
		return observability.WorkflowMetadata{SessionID: sessionID}
	}
	return observability.WorkflowMetadata{
		ID:          wf.StartExecutorID(),
		Name:        wf.Name(),
		Description: wf.Description(),
		SessionID:   sessionID,
	}
}

func contextWithWorkflowTelemetry(ctx context.Context, wf *workflow.Workflow) context.Context {
	if wf == nil {
		return observability.ContextWithTelemetry(ctx, nil)
	}
	return wf.ContextWithTelemetry(ctx)
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

// waitForInput: blocks until the input signal fires or ctx is canceled.
func (iw *inputWaiter) waitForInput(ctx context.Context) error {
	select {
	case <-iw.signal:
		return nil // Got signal
	case <-ctx.Done():
		return ctx.Err() // Cancellation
	}
}

// close closes the inputWaiter and releases any waiting goroutines
func (iw *inputWaiter) close() {
	if iw.closed.CompareAndSwap(false, true) {
		close(iw.signal)
	}
}

var _ RunEventStream = (*streamingRunEventStream)(nil)

// streamingRunEventStream streams events as they are created, buffering them
// until a consumer reads from the stream.
type streamingRunEventStream struct {
	eventQueue concurrent.Queue[workflow.Event]
	eventReady chan struct{}

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
		eventReady:     make(chan struct{}, 1),
	}
}

func (s *streamingRunEventStream) Start() {
	// Start the background run loop that drives superstep execution
	if s.disableRunLoop {
		close(s.runLoopDone)
		return
	}
	go s.runLoop()
}

func (s *streamingRunEventStream) runLoop() {
	wf := s.stepRunner.Workflow()
	ctx := contextWithWorkflowTelemetry(s.runLoopCtx, wf)
	telemetry := observability.FromContext(ctx)
	defer close(s.runLoopDone)

	ctx, sessionActivity := telemetry.StartWorkflowSession(ctx, workflowMetadata(wf, s.stepRunner.SessionID()))
	var sessionErr error
	defer func() {
		if sessionErr != nil {
			sessionActivity.AddErrorEvent(observability.EventSessionError, sessionErr)
			sessionActivity.CaptureError(sessionErr)
		} else {
			sessionActivity.AddEvent(observability.EventSessionCompleted)
		}
		sessionActivity.End()
	}()

	defer s.runLoopCancel()
	defer s.setStatus(RunStatusEnded)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Subscribe to events - they will flow directly to the channel as they're raised
	eventSink := s.stepRunner.OutgoingEvents()
	eventHandler := s.onEventRaised
	eventSink.AddHandler(eventHandler)
	defer eventSink.RemoveHandler(eventHandler)
	if err := s.stepRunner.RepublishPendingEvents(ctx); err != nil {
		sessionErr = err
		s.sendEvent(ctx, workflow.ErrorEvent{Error: err})
		cancel()
		return
	}

	// Wait for the first input before starting.
	// The consumer will call EnqueueMessage which signals the run loop.
	// Note: RunHandle also signals here on checkpoint resume when there are
	// already pending requests, so the first iteration can emit a
	// PendingRequests halt signal even without unprocessed messages.
	if err := s.inputWaiter.waitForInput(ctx); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		cycleCtx, runActivity := telemetry.StartWorkflowRun(ctx, workflowMetadata(wf, s.stepRunner.SessionID()))
		runActivity.AddEvent(observability.EventWorkflowStarted)

		// Run all available supersteps continuously
		// Events are streamed out in real-time as they happen via the event handler
		if s.stepRunner.HasUnprocessedMessages() {
			// Flip to Running only when there's actual work to process.
			// This is intentionally inside the HasUnprocessedMessages branch so
			// that stale input signals cannot transiently flip status back to
			// Running after a prior halt has already been observed by callers
			// (e.g. Run.RunToNextHalt returning after reading an Idle halt signal).
			s.setStatus(RunStatusRunning)

			// Emit StartedEvent only when there's actual work to process,
			// to avoid spurious events on no-work loop iterations.
			s.sendEvent(cycleCtx, workflow.StartedEvent{})

			for s.stepRunner.HasUnprocessedMessages() {
				select {
				case <-cycleCtx.Done():
					runActivity.End()
					return
				default:
				}

				if _, err := s.stepRunner.RunSuperStep(cycleCtx); err != nil {
					runActivity.AddErrorEvent(observability.EventWorkflowError, err)
					runActivity.CaptureError(err)
					runActivity.End()
					sessionErr = err
					// Send error event
					s.sendEvent(cycleCtx, workflow.ErrorEvent{Error: err})
					cancel()
					return
				}
			}
		}
		runActivity.AddEvent(observability.EventWorkflowCompleted)
		runActivity.End()

		// Update status based on what's waiting
		if s.stepRunner.HasUnservicedRequests() {
			s.setStatus(RunStatusPendingRequests)
		} else {
			s.setStatus(RunStatusIdle)
		}

		// Signal completion to consumer so they can check status and decide whether to continue
		// Increment epoch so next consumer iteration gets a new completion signal
		currentEpoch := s.completionEpoch.Add(1)
		capturedStatus := s.getStatus()

		// Send internal halt signal
		if err := s.enqueueEvent(ctx, &internalHaltSignal{epoch: currentEpoch, status: capturedStatus}); err != nil {
			return
		}

		// Wait for next input from the consumer.
		// Works for both Idle (no work) and PendingRequests (waiting for
		// responses). The wait is unbounded; signals will wake it.
		if err := s.inputWaiter.waitForInput(ctx); err != nil {
			return
		}
	}
}

func (s *streamingRunEventStream) onEventRaised(ctx context.Context, sender any, evt workflow.Event) error {
	if err := s.enqueueEvent(ctx, evt); err != nil {
		return err
	}
	if _, ok := evt.(workflow.ErrorEvent); ok {
		s.runLoopCancel()
		return nil
	}
	return s.runLoopCtx.Err()
}

func (s *streamingRunEventStream) sendEvent(ctx context.Context, evt workflow.Event) {
	_ = s.enqueueEvent(ctx, evt)
}

func (s *streamingRunEventStream) enqueueEvent(ctx context.Context, evt workflow.Event) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := s.runLoopCtx.Err(); err != nil {
		return err
	}
	s.eventQueue.Enqueue(evt)
	s.signalEventReady()
	return nil
}

func (s *streamingRunEventStream) signalEventReady() {
	select {
	case s.eventReady <- struct{}{}:
	default:
	}
}

func (s *streamingRunEventStream) nextEvent(ctx context.Context) (workflow.Event, bool) {
	for {
		if evt, ok := s.eventQueue.Dequeue(); ok {
			return evt, true
		}

		select {
		case <-ctx.Done():
			return nil, false
		case <-s.eventReady:
		case <-s.runLoopCtx.Done():
			if evt, ok := s.eventQueue.Dequeue(); ok {
				return evt, true
			}
			return nil, false
		}
	}
}

// SignalInput signals that new input has been provided and the run loop should continue processing.
// Called by RunHandle when the user enqueues a message or response.
func (s *streamingRunEventStream) SignalInput() {
	s.inputWaiter.signalInput()
}

func (s *streamingRunEventStream) TakeEventStream(ctx context.Context, blockOnPendingRequest bool) iter.Seq2[workflow.Event, error] {
	// Decide which completion epoch to expect. If the run loop has fresh
	// work (or is currently running), we want the *next* completion signal;
	// otherwise the run has already halted and we should consume the halt
	// signal that was emitted before this consumer arrived.
	currentEpoch := s.completionEpoch.Load()
	expectingFreshWork := s.stepRunner.HasUnprocessedMessages() || s.getStatus() == RunStatusRunning
	var myEpoch int64
	if expectingFreshWork {
		myEpoch = currentEpoch + 1
	} else {
		myEpoch = currentEpoch
	}

	return func(yield func(workflow.Event, error) bool) {
		for {
			evt, ok := s.nextEvent(ctx)
			if !ok {
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
				if signal.status == RunStatusIdle || signal.status == RunStatusEnded {
					return
				}

				if !blockOnPendingRequest && signal.status == RunStatusPendingRequests {
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

func (s *streamingRunEventStream) GetStatus(ctx context.Context) (RunStatus, error) {
	return s.getStatus(), nil
}

func (s *streamingRunEventStream) getStatus() RunStatus {
	return RunStatus(s.runStatus.Load())
}

func (s *streamingRunEventStream) setStatus(status RunStatus) {
	s.runStatus.Store(int32(status))
}

// ClearBufferedEvents clears all buffered events from the queue.
// This should be called when restoring a checkpoint to discard stale events from superseded supersteps.
func (s *streamingRunEventStream) ClearBufferedEvents() {
	for {
		if _, ok := s.eventQueue.Dequeue(); !ok {
			break
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
	status RunStatus
}

func (s *internalHaltSignal) Data() any {
	return s
}

var _ RunEventStream = (*lockstepRunEventStream)(nil)

// lockstepRunEventStream is a synchronous implementation of RunEventStream that collects events
// during superstep execution and yields them after each step completes.
type lockstepRunEventStream struct {
	stepRunner   SuperStepRunner
	inputWaiter  inputWaiter
	stopCtx      context.Context
	stopCancel   context.CancelFunc
	runStatus    atomic.Int32 // stores RunStatus
	eventQueue   concurrent.Queue[workflow.Event]
	eventHandler func(context.Context, any, workflow.Event) error
}

func newLockstepRunEventStream(stepRunner SuperStepRunner) *lockstepRunEventStream {
	ctx, cancel := context.WithCancel(context.Background())
	l := &lockstepRunEventStream{
		stepRunner:  stepRunner,
		inputWaiter: newInputWaiter(),
		stopCtx:     ctx,
		stopCancel:  cancel,
	}
	l.eventHandler = func(ctx context.Context, sender any, evt workflow.Event) error {
		l.eventQueue.Enqueue(evt)
		return nil
	}
	stepRunner.OutgoingEvents().AddHandler(l.eventHandler)
	return l
}

func (l *lockstepRunEventStream) Start() {
	// No-op for lockstep execution
}

func (l *lockstepRunEventStream) GetStatus(ctx context.Context) (RunStatus, error) {
	return l.getStatus(), nil
}

func (l *lockstepRunEventStream) getStatus() RunStatus {
	return RunStatus(l.runStatus.Load())
}

func (l *lockstepRunEventStream) setStatus(status RunStatus) {
	l.runStatus.Store(int32(status))
}

func (l *lockstepRunEventStream) TakeEventStream(ctx context.Context, blockOnPendingRequest bool) iter.Seq2[workflow.Event, error] {
	return func(yield func(workflow.Event, error) bool) {
		wf := l.stepRunner.Workflow()
		ctx := contextWithWorkflowTelemetry(ctx, wf)
		telemetry := observability.FromContext(ctx)
		ctx, sessionActivity := telemetry.StartWorkflowSession(ctx, workflowMetadata(wf, l.stepRunner.SessionID()))
		var sessionErr error
		defer func() {
			if sessionErr != nil {
				sessionActivity.AddErrorEvent(observability.EventSessionError, sessionErr)
				sessionActivity.CaptureError(sessionErr)
			} else {
				sessionActivity.AddEvent(observability.EventSessionCompleted)
			}
			sessionActivity.End()
		}()

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

		defer func() {
			// Update status
			if l.stepRunner.HasUnservicedRequests() {
				l.setStatus(RunStatusPendingRequests)
			} else {
				l.setStatus(RunStatusIdle)
			}
		}()

		l.setStatus(RunStatusRunning)

		var runActivity *observability.Activity
		cycleCtx := linkedCtx
		startRunActivity := func() {
			cycleCtx, runActivity = telemetry.StartWorkflowRun(linkedCtx, workflowMetadata(wf, l.stepRunner.SessionID()))
			runActivity.AddEvent(observability.EventWorkflowStarted)
		}

		startRunActivity()
		l.eventQueue.Enqueue(workflow.StartedEvent{})

		if err := l.stepRunner.RepublishPendingEvents(linkedCtx); err != nil {
			sessionErr = err
			yield(nil, err)
			return
		}
		if !l.drainAndFilterEvents(linkedCtx, yield) {
			return
		}

		for {
			// Run all available supersteps
			for l.stepRunner.HasUnprocessedMessages() && cycleCtx.Err() == nil {
				if _, err := l.stepRunner.RunSuperStep(cycleCtx); err != nil {
					if runActivity != nil {
						runActivity.AddErrorEvent(observability.EventWorkflowError, err)
						runActivity.CaptureError(err)
						runActivity.End()
					}
					sessionErr = err
					if !errors.Is(err, context.Canceled) {
						yield(nil, err)
					}
					return
				}

				if !l.drainAndFilterEvents(linkedCtx, yield) {
					return
				}
			}
			if runActivity != nil {
				runActivity.AddEvent(observability.EventWorkflowCompleted)
				runActivity.End()
				runActivity = nil
			}

			// Update status
			if l.stepRunner.HasUnservicedRequests() {
				l.setStatus(RunStatusPendingRequests)
			} else {
				l.setStatus(RunStatusIdle)
			}

			// Check if we should break
			status := l.getStatus()
			if l.shouldBreak(status, blockOnPendingRequest, linkedCtx) {
				return
			}

			// If blocking on pending requests and we have pending requests, wait for input
			if blockOnPendingRequest && status == RunStatusPendingRequests {
				if err := l.inputWaiter.waitForInput(linkedCtx); err != nil {
					return
				}
				startRunActivity()
				// Emit a StartedEvent for the continuation cycle, mirroring the
				// streaming run loop which raises one per input → processing →
				// halt cycle. Only when there is actual work to process, so
				// no-work wakeups (e.g. spurious signals) stay event-free. The
				// event is drained and yielded before the cycle's supersteps.
				if l.stepRunner.HasUnprocessedMessages() {
					l.eventQueue.Enqueue(workflow.StartedEvent{})
					// Drain immediately so the StartedEvent is yielded before the
					// cycle's supersteps run, rather than being held in the queue
					// and drained alongside the first superstep's events.
					if !l.drainAndFilterEvents(linkedCtx, yield) {
						return
					}
				}
			} else {
				// No more work to do
				return
			}
		}
	}
}

func (l *lockstepRunEventStream) drainAndFilterEvents(ctx context.Context, yield func(workflow.Event, error) bool) bool {
	var hadRequestHaltEvent bool
	for {
		evt, ok := l.eventQueue.Dequeue()
		if !ok {
			break
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return false
		}
		if _, ok := evt.(workflow.RequestHaltEvent); ok {
			hadRequestHaltEvent = true
			continue
		}
		if !yield(evt, nil) {
			return false
		}
	}

	return !hadRequestHaltEvent && ctx.Err() == nil
}

func (l *lockstepRunEventStream) shouldBreak(status RunStatus, blockOnPendingRequest bool, ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	if status == RunStatusIdle || status == RunStatusEnded {
		return true
	}
	if status == RunStatusPendingRequests && !blockOnPendingRequest {
		return true
	}
	return false
}

// SignalInput signals that new input has been provided and the run loop should continue processing.
func (l *lockstepRunEventStream) SignalInput() {
	l.inputWaiter.signalInput()
}

func (l *lockstepRunEventStream) ClearBufferedEvents() {
	for {
		if _, ok := l.eventQueue.Dequeue(); !ok {
			break
		}
	}
}

func (l *lockstepRunEventStream) Stop() {
	l.stopCancel()
	l.stepRunner.OutgoingEvents().RemoveHandler(l.eventHandler)
	// Close the input waiter
	l.inputWaiter.close()
}
