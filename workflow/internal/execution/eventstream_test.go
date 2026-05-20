// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/workflow"
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
	outgoingEvents *ConcurrentEventSink
}

func newTestSuperStepRunner() *testSuperStepRunner {
	return &testSuperStepRunner{outgoingEvents: &ConcurrentEventSink{}}
}

func (r *testSuperStepRunner) SessionID() string { return "session" }

func (r *testSuperStepRunner) Workflow() *workflow.Workflow {
	wf, err := workflow.NewBuilder(workflow.BindExecutor(&workflow.Executor{ID: "start"})).Build()
	if err != nil {
		panic(err)
	}
	return wf
}

func (r *testSuperStepRunner) StartExecutorID() string { return "start" }

func (r *testSuperStepRunner) HasUnservicedRequests() bool { return false }

func (r *testSuperStepRunner) HasUnprocessedMessages() bool { return false }

func (r *testSuperStepRunner) RepublishPendingEvents(context.Context) error { return nil }

func (r *testSuperStepRunner) EnqueueResponse(context.Context, *workflow.ExternalResponse) error {
	return nil
}

func (r *testSuperStepRunner) IsValidInputType(context.Context, reflect.Type) bool { return true }

func (r *testSuperStepRunner) EnqueueMessage(context.Context, any) error { return nil }

func (r *testSuperStepRunner) OutgoingEvents() *ConcurrentEventSink { return r.outgoingEvents }

func (r *testSuperStepRunner) RunSuperStep(context.Context) (bool, error) { return false, nil }

func (r *testSuperStepRunner) RequestEndRun(context.Context) error { return nil }

func (r *testSuperStepRunner) ResponsePortExecutorID(string) (string, bool) { return "", false }
