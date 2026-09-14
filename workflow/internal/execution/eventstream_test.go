// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/observability"
	"github.com/microsoft/agent-framework-go/workflow/internal/workflowtest"
)

func TestStreamingRunEventStream_RemovesEventHandlerOnStop(t *testing.T) {
	runner := newTestSuperStepRunner()
	stream := newStreamingRunEventStream(runner, false)

	stream.Start()
	waitForEventHandlerCount(t, runner.outgoingEvents, 1)

	stream.Stop()
	waitForEventHandlerCount(t, runner.outgoingEvents, 0)
}

func TestStreamingRunEventStream_ErrorEventCancelsRunLoop(t *testing.T) {
	runner := newTestSuperStepRunner()
	stream := newStreamingRunEventStream(runner, false)

	stream.Start()
	defer stream.Stop()
	waitForEventHandlerCount(t, runner.outgoingEvents, 1)

	errorEvent := workflow.ErrorEvent{Error: errors.New("boom")}
	if err := runner.outgoingEvents.Enqueue(context.Background(), errorEvent); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	select {
	case <-stream.runLoopDone:
	case <-time.After(time.Second):
		t.Fatal("run loop did not stop after ErrorEvent")
	}
	if got := stream.getStatus(); got != RunStatusEnded {
		t.Fatalf("status after ErrorEvent = %v, want Ended", got)
	}

	evt, ok := stream.nextEvent(context.Background())
	if !ok {
		t.Fatal("expected ErrorEvent to remain readable")
	}
	if got, ok := evt.(workflow.ErrorEvent); !ok || got.Error != errorEvent.Error {
		t.Fatalf("next event = %#v, want ErrorEvent", evt)
	}
	waitForEventHandlerCount(t, runner.outgoingEvents, 0)
}

func TestStreamingRunEventStream_NoWorkWakeupDoesNotOpenWorkflowRunSpan(t *testing.T) {
	tracer := workflowtest.NewRecordingTracer()
	wf, err := workflow.NewBuilder((&workflow.Executor{ID: "start", ImplementationID: "workflow_test.start"}).Bind()).
		WithTelemetry(tracer, workflow.TelemetryOptions{}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	runner := newTestSuperStepRunner()
	runner.wf = wf
	stream := newStreamingRunEventStream(runner, false)

	stream.Start()
	defer stream.Stop()
	waitForEventHandlerCount(t, runner.outgoingEvents, 1)

	// Trigger a no-work wakeup: the run loop is signaled but
	// HasUnprocessedMessages reports false, so the iteration has nothing to
	// process. Such an iteration must not open a WorkflowRun span, matching
	// the lockstep implementation.
	stream.SignalInput()

	// The loop enqueues an internalHaltSignal once the (no-work) cycle
	// completes; use it to synchronize before asserting on spans.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	evt, ok := stream.nextEvent(ctx)
	if !ok {
		t.Fatal("run loop did not complete the no-work iteration")
	}
	if _, ok := evt.(*internalHaltSignal); !ok {
		t.Fatalf("unexpected event on no-work wakeup: %#v", evt)
	}

	if count := workflowtest.CountSpansWithPrefix(tracer.Spans(), observability.ActivityWorkflowInvoke); count != 0 {
		t.Fatalf("workflow_invoke span count = %d, want 0 on no-work wakeup", count)
	}
}

func TestStreamingRunEventStream_OlderHaltDoesNotCompleteNewInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := newTestSuperStepRunner()
		var workReady atomic.Bool
		var haltCount atomic.Int32
		finishingOldCycle := make(chan struct{})
		finishOldCycle := make(chan struct{})
		processNewInput := make(chan struct{})
		runner.hasUnprocessedFn = workReady.Load
		runner.hasUnservicedFn = func() bool {
			if haltCount.Add(1) == 1 {
				// Pause after the old cycle has checked for work, but before it
				// publishes its completion. A response arrives in this gap.
				close(finishingOldCycle)
				<-finishOldCycle
				return true
			}
			return false
		}
		runner.runSuperStepFn = func(ctx context.Context) (bool, error) {
			select {
			case <-processNewInput:
			case <-ctx.Done():
				return false, ctx.Err()
			}
			workReady.Store(false)
			return true, runner.outgoingEvents.Enqueue(ctx, workflow.OutputEvent{Output: "response"})
		}

		stream := newStreamingRunEventStream(runner, false)
		stream.Start()
		defer stream.Stop()
		stream.SignalInput()
		<-finishingOldCycle

		workReady.Store(true)
		stream.SignalInput()
		done := make(chan struct{})
		var outputs int
		go func() {
			defer close(done)
			for evt, err := range stream.TakeEventStream(t.Context(), false) {
				if err != nil {
					t.Errorf("TakeEventStream: %v", err)
					return
				}
				if _, ok := evt.(workflow.OutputEvent); ok {
					outputs++
				}
			}
		}()
		synctest.Wait()

		close(finishOldCycle)
		synctest.Wait()
		select {
		case <-done:
			t.Error("the previous cycle's halt completed the stream before the new input was processed")
		default:
		}

		close(processNewInput)
		<-done
		if outputs != 1 {
			t.Errorf("output count = %d, want 1", outputs)
		}
	})
}

func TestStreamingRunEventStream_NoWorkSignalWaitsForFreshHalt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := newTestSuperStepRunner()
		var restoring atomic.Bool
		finishingNewCycle := make(chan struct{})
		finishNewCycle := make(chan struct{})
		runner.hasUnservicedFn = func() bool {
			if restoring.Swap(false) {
				close(finishingNewCycle)
				<-finishNewCycle
				return true
			}
			return false
		}

		stream := newStreamingRunEventStream(runner, false)
		stream.Start()
		defer stream.Stop()
		stream.SignalInput()
		for _, err := range stream.TakeEventStream(t.Context(), false) {
			if err != nil {
				t.Fatalf("initial TakeEventStream: %v", err)
			}
		}

		// Restoring pending requests signals a cycle even when there are no
		// queued messages. The old Idle status must not complete this wait.
		synctest.Wait()
		restoring.Store(true)
		stream.SignalInput()
		<-finishingNewCycle
		done := make(chan struct{})
		go func() {
			defer close(done)
			for _, err := range stream.TakeEventStream(t.Context(), false) {
				if err != nil {
					t.Errorf("TakeEventStream: %v", err)
				}
			}
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Error("the previous Idle status completed the stream before the signaled cycle halted")
		default:
		}

		close(finishNewCycle)
		<-done
	})
}

func TestStreamingRunEventStream_RepeatedReadsAfterHalt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status RunStatus
	}{
		{"idle", RunStatusIdle},
		{"pending_requests", RunStatusPendingRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				status := tc.status
				runner := newTestSuperStepRunner()
				var workReady atomic.Bool
				workReady.Store(true)
				runner.hasUnprocessedFn = workReady.Load
				runner.hasUnservicedFn = func() bool { return status == RunStatusPendingRequests }
				runner.runSuperStepFn = func(ctx context.Context) (bool, error) {
					workReady.Store(false)
					return true, runner.outgoingEvents.Enqueue(ctx, workflow.OutputEvent{Output: "response"})
				}

				stream := newStreamingRunEventStream(runner, false)
				stream.Start()
				defer stream.Stop()
				stream.SignalInput()

				for read := range 3 {
					// A virtual deadline makes a missing halt fail without relying
					// on how quickly the CI host schedules the run loop.
					ctx, cancel := context.WithTimeout(t.Context(), time.Second)
					var events int
					for _, err := range stream.TakeEventStream(ctx, false) {
						if err != nil {
							t.Fatalf("read %d: %v", read, err)
						}
						events++
					}
					err := ctx.Err()
					cancel()
					if err != nil {
						t.Fatalf("read %d did not observe a halt: %v", read, err)
					}
					want := 0
					if read == 0 {
						want = 2 // StartedEvent and OutputEvent, once only.
					}
					if events != want {
						t.Errorf("read %d event count = %d, want %d", read, events, want)
					}
					if got := stream.getStatus(); got != status {
						t.Errorf("read %d status = %v, want %v", read, got, status)
					}
					synctest.Wait()
				}
			})
		})
	}
}

func TestStreamingRunEventStream_WatchBeforeInputWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := newTestSuperStepRunner()
		var workReady atomic.Bool
		runner.hasUnprocessedFn = workReady.Load
		runner.runSuperStepFn = func(ctx context.Context) (bool, error) {
			workReady.Store(false)
			return true, runner.outgoingEvents.Enqueue(ctx, workflow.OutputEvent{Output: "response"})
		}
		stream := newStreamingRunEventStream(runner, false)
		stream.Start()
		defer stream.Stop()

		done := make(chan struct{})
		var outputs int
		go func() {
			defer close(done)
			for evt, err := range stream.TakeEventStream(t.Context(), false) {
				if err != nil {
					t.Errorf("TakeEventStream: %v", err)
					return
				}
				if _, ok := evt.(workflow.OutputEvent); ok {
					outputs++
				}
			}
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("watch halted before any input was supplied")
		default:
		}

		workReady.Store(true)
		stream.SignalInput()
		<-done
		if outputs != 1 {
			t.Errorf("output count = %d, want 1", outputs)
		}
	})
}

func TestStreamingRunEventStream_PendingWatchWaitsForResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := newTestSuperStepRunner()
		var workReady, pending atomic.Bool
		pending.Store(true)
		runner.hasUnprocessedFn = workReady.Load
		runner.hasUnservicedFn = pending.Load
		runner.runSuperStepFn = func(ctx context.Context) (bool, error) {
			workReady.Store(false)
			pending.Store(false)
			return true, runner.outgoingEvents.Enqueue(ctx, workflow.OutputEvent{Output: "response"})
		}
		stream := newStreamingRunEventStream(runner, false)
		stream.Start()
		defer stream.Stop()
		stream.SignalInput()
		for _, err := range stream.TakeEventStream(t.Context(), false) {
			if err != nil {
				t.Fatalf("initial TakeEventStream: %v", err)
			}
		}
		synctest.Wait()

		done := make(chan struct{})
		var outputs int
		go func() {
			defer close(done)
			for evt, err := range stream.TakeEventStream(t.Context(), true) {
				if err != nil {
					t.Errorf("TakeEventStream: %v", err)
					return
				}
				if _, ok := evt.(workflow.OutputEvent); ok {
					outputs++
				}
			}
		}()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("blocking watch halted before the pending request was serviced")
		default:
		}

		workReady.Store(true)
		stream.SignalInput()
		<-done
		if outputs != 1 {
			t.Errorf("output count = %d, want 1", outputs)
		}
	})
}

func TestLockstepRunEventStream_EarlyStopEndsWorkflowRunSpan(t *testing.T) {
	tracer := workflowtest.NewRecordingTracer()
	wf, err := workflow.NewBuilder((&workflow.Executor{ID: "start", ImplementationID: "workflow_test.start"}).Bind()).
		WithTelemetry(tracer, workflow.TelemetryOptions{}).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	runner := newTestSuperStepRunner()
	runner.wf = wf
	stream := newLockstepRunEventStream(runner)
	defer stream.Stop()

	for range stream.TakeEventStream(t.Context(), false) {
		break
	}

	span := workflowtest.FindSpanWithPrefix(t, tracer.Spans(), observability.ActivityWorkflowInvoke)
	span.RequireEnded(t)
}

// TestLockstepRunEventStream_StaleWakeupBeforeWorkStillEmitsStartedEvent is a
// regression test for a race in the lockstep continuation cycle. The input
// signal is a binary semaphore, so a stale/leftover signal can wake the run
// loop while HasUnprocessedMessages still reports false, with the real work
// arriving an instant later. Previously the StartedEvent for the continuation
// cycle was gated on a single HasUnprocessedMessages check taken right after
// the wakeup; when that check observed the stale (no-work) wakeup, the event
// was skipped even though the outer loop then processed the work — dropping a
// StartedEvent. The stream must emit exactly one StartedEvent per cycle that
// actually runs: the initial cycle and the continuation cycle → two total.
func TestLockstepRunEventStream_StaleWakeupBeforeWorkStillEmitsStartedEvent(t *testing.T) {
	runner := newTestSuperStepRunner()

	// phase drives a single input → request → response → output run.
	// The bug is a gap between two reads of HasUnprocessedMessages in the
	// continuation block: the read that decides whether to emit the
	// StartedEvent, and the later read that decides whether to process the
	// work. We model the response arriving in exactly that gap: once the run
	// is blocked on the pending request (observed via HasUnservicedRequests)
	// the first subsequent HasUnprocessedMessages read reports "no work" but
	// arms the work so the very next read reports "work available".
	var (
		phase     int
		workReady = true // the initial kick is ready to process
		pending   bool
		inBlock   bool
	)

	runner.hasUnprocessedFn = func() bool {
		if inBlock && pending && !workReady {
			// Simulate the response landing in the gap: this decision read sees
			// no work, but work becomes available immediately afterwards. The
			// response also services the pending request, so from now on
			// HasUnservicedRequests reports false — exercising the requirement
			// that the loop break to process the work rather than recomputing
			// status (which would otherwise flip to Idle and drop the cycle).
			workReady = true
			pending = false
			return false
		}
		return workReady
	}
	runner.hasUnservicedFn = func() bool {
		if pending {
			// The run has reached the point where it blocks on the pending
			// request; from here the race window is open.
			inBlock = true
		}
		return pending
	}
	runner.runSuperStepFn = func(ctx context.Context) (bool, error) {
		switch phase {
		case 0:
			// Initial cycle: post an external request and halt.
			_ = runner.outgoingEvents.Enqueue(ctx, workflow.RequestInfoEvent{})
			workReady = false
			pending = true
			phase = 1
		default:
			// Continuation cycle: deliver the response as output.
			_ = runner.outgoingEvents.Enqueue(ctx, workflow.OutputEvent{})
			workReady = false
			pending = false
			phase = 2
		}
		return true, nil
	}

	stream := newLockstepRunEventStream(runner)
	// Pre-load a stale signal so the first waitForInput in the continuation
	// block returns immediately with no work available, deterministically
	// exercising the stale-wakeup path.
	stream.SignalInput()
	defer stream.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var startedCount, outputCount int
	for evt, err := range stream.TakeEventStream(ctx, true) {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		switch evt.(type) {
		case workflow.StartedEvent:
			startedCount++
		case workflow.OutputEvent:
			outputCount++
		}
	}

	if startedCount != 2 {
		t.Errorf("StartedEvent count = %d, want 2 (one per cycle that runs)", startedCount)
	}
	if outputCount != 1 {
		t.Errorf("output event count = %d, want 1", outputCount)
	}
}

type declaredEnqueueMessage interface {
	declaredEnqueueMessage()
}

type concreteEnqueueMessage struct{}

func (concreteEnqueueMessage) declaredEnqueueMessage() {}

func TestRunHandle_EnqueueMessageUntyped_DefaultsToRuntimeType(t *testing.T) {
	runner := newTestSuperStepRunner()
	handle := newTestRunHandle(t, runner)
	message := concreteEnqueueMessage{}

	accepted, err := handle.EnqueueMessageUntyped(t.Context(), message, nil)
	if err != nil || !accepted {
		t.Fatalf("EnqueueMessageUntyped() = (%v, %v), want (true, nil)", accepted, err)
	}
	if runner.enqueuedMessage != message || runner.enqueuedType != reflect.TypeFor[concreteEnqueueMessage]() {
		t.Fatalf("enqueued message/type = (%#v, %v), want runtime concrete type", runner.enqueuedMessage, runner.enqueuedType)
	}
}

func TestRunHandle_EnqueueMessageUntyped_ForwardsDeclaredType(t *testing.T) {
	runner := newTestSuperStepRunner()
	handle := newTestRunHandle(t, runner)
	message := concreteEnqueueMessage{}
	declaredType := reflect.TypeFor[declaredEnqueueMessage]()

	accepted, err := handle.EnqueueMessageUntyped(t.Context(), message, declaredType)
	if err != nil || !accepted {
		t.Fatalf("EnqueueMessageUntyped() = (%v, %v), want (true, nil)", accepted, err)
	}
	if runner.enqueuedType != declaredType {
		t.Fatalf("enqueued type = %v, want %v", runner.enqueuedType, declaredType)
	}
}

func TestRunHandle_EnqueueMessageUntyped_RejectsIncompatibleDeclaredType(t *testing.T) {
	runner := newTestSuperStepRunner()
	handle := newTestRunHandle(t, runner)

	if _, err := handle.EnqueueMessageUntyped(t.Context(), "message", reflect.TypeFor[int]()); err == nil {
		t.Fatal("EnqueueMessageUntyped() error = nil, want incompatible declared type error")
	}
	if runner.enqueuedMessage != nil {
		t.Fatal("step runner received an incompatible message")
	}
}

func TestRunHandle_EnqueueMessageUntyped_UsesDeclaredTypeForExternalResponse(t *testing.T) {
	response := &workflow.ExternalResponse{}

	externalRunner := newTestSuperStepRunner()
	externalHandle := newTestRunHandle(t, externalRunner)
	accepted, err := externalHandle.EnqueueMessageUntyped(t.Context(), response, reflect.TypeFor[*workflow.ExternalResponse]())
	if err != nil || !accepted {
		t.Fatalf("external response enqueue = (%v, %v), want (true, nil)", accepted, err)
	}
	if externalRunner.enqueuedResponse != response || externalRunner.enqueuedMessage != nil {
		t.Fatal("ExternalResponse declared type did not use the response path")
	}

	objectRunner := newTestSuperStepRunner()
	objectHandle := newTestRunHandle(t, objectRunner)
	accepted, err = objectHandle.EnqueueMessageUntyped(t.Context(), response, reflect.TypeFor[any]())
	if err != nil || !accepted {
		t.Fatalf("object-declared response enqueue = (%v, %v), want (true, nil)", accepted, err)
	}
	if objectRunner.enqueuedResponse != nil || objectRunner.enqueuedMessage != response || objectRunner.enqueuedType != reflect.TypeFor[any]() {
		t.Fatal("ExternalResponse declared as any did not use the ordinary message path")
	}
}

func newTestRunHandle(t *testing.T, runner *testSuperStepRunner) *RunHandle {
	t.Helper()
	stream := newLockstepRunEventStream(runner)
	t.Cleanup(stream.Stop)
	return &RunHandle{
		stepRunner:  runner,
		eventStream: stream,
		endRunCtx:   context.Background(),
	}
}

func waitForEventHandlerCount(t *testing.T, sink *ConcurrentEventSink, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if got := sink.HandlerCount(); got == want {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("EventRaised handler count = %d, want %d", sink.HandlerCount(), want)
		}
	}
}

type testSuperStepRunner struct {
	outgoingEvents   *ConcurrentEventSink
	wf               *workflow.Workflow
	enqueuedMessage  any
	enqueuedType     reflect.Type
	enqueuedResponse *workflow.ExternalResponse

	// Optional hooks. When set, they override the default behavior. They let
	// tests script a run (e.g. an input → request → response → output cycle)
	// deterministically.
	hasUnservicedFn  func() bool
	hasUnprocessedFn func() bool
	runSuperStepFn   func(context.Context) (bool, error)
}

func newTestSuperStepRunner() *testSuperStepRunner {
	return &testSuperStepRunner{outgoingEvents: &ConcurrentEventSink{}}
}

func (r *testSuperStepRunner) SessionID() string { return "session" }

func (r *testSuperStepRunner) Workflow() *workflow.Workflow {
	if r.wf != nil {
		return r.wf
	}
	wf, err := workflow.NewBuilder((&workflow.Executor{ID: "start", ImplementationID: "workflow_test.start"}).Bind()).Build()
	if err != nil {
		panic(err)
	}
	return wf
}

func (r *testSuperStepRunner) StartExecutorID() string { return "start" }

func (r *testSuperStepRunner) HasUnservicedRequests() bool {
	if r.hasUnservicedFn != nil {
		return r.hasUnservicedFn()
	}
	return false
}

func (r *testSuperStepRunner) HasUnprocessedMessages() bool {
	if r.hasUnprocessedFn != nil {
		return r.hasUnprocessedFn()
	}
	return false
}

func (r *testSuperStepRunner) RepublishPendingEvents(context.Context) error { return nil }

func (r *testSuperStepRunner) EnqueueResponse(_ context.Context, response *workflow.ExternalResponse) error {
	r.enqueuedResponse = response
	return nil
}

func (r *testSuperStepRunner) IsValidInputType(context.Context, reflect.Type) (bool, error) {
	return true, nil
}

func (r *testSuperStepRunner) EnqueueMessageUntyped(_ context.Context, message any, declaredType reflect.Type) (bool, error) {
	r.enqueuedMessage = message
	r.enqueuedType = declaredType
	return true, nil
}

func (r *testSuperStepRunner) OutgoingEvents() *ConcurrentEventSink { return r.outgoingEvents }

func (r *testSuperStepRunner) RunSuperStep(ctx context.Context) (bool, error) {
	if r.runSuperStepFn != nil {
		return r.runSuperStepFn(ctx)
	}
	return false, nil
}

func (r *testSuperStepRunner) RequestEndRun(context.Context) error { return nil }

func (r *testSuperStepRunner) ResponsePortExecutorID(string) (string, bool) { return "", false }
