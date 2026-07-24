// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func minimalEchoBinding(id string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, _ any) (any, error) {
					return nil, ctx.YieldOutput("ok")
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}

func TestStartedEvent_EmittedBeforeSuperStepStarted_OffThread(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	run, err := inproc.Default.Run(context.Background(), wf, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	startedAt := -1
	stepAt := -1
	i := 0
	for evt := range run.OutgoingEvents() {
		switch evt.(type) {
		case workflow.StartedEvent:
			if startedAt < 0 {
				startedAt = i
			}
		case workflow.SuperStepStartedEvent:
			if stepAt < 0 {
				stepAt = i
			}
		}
		i++
	}
	if startedAt < 0 {
		t.Fatalf("expected a StartedEvent, got none")
	}
	if stepAt < 0 {
		t.Fatalf("expected a SuperStepStartedEvent, got none")
	}
	if startedAt >= stepAt {
		t.Errorf("StartedEvent at %d should precede SuperStepStartedEvent at %d", startedAt, stepAt)
	}
}

func TestStartedEvent_EmittedInLockstepMode(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	run, err := inproc.Lockstep.Run(context.Background(), wf, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawStarted bool
	for evt := range run.OutgoingEvents() {
		if _, ok := evt.(workflow.StartedEvent); ok {
			sawStarted = true
			break
		}
	}
	if !sawStarted {
		t.Errorf("expected a StartedEvent in Lockstep mode")
	}
}

func TestStartedEvent_NotEmittedWhenNoWork(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamingMessage(t, stream, ctx, "first")

	var startedCount int
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		if _, ok := evt.(workflow.StartedEvent); ok {
			startedCount++
		}
	}
	if startedCount != 1 {
		t.Errorf("expected exactly 1 StartedEvent, got %d", startedCount)
	}
}

func TestStartedEvent_EmittedInLockstepWithoutWork(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	var sawStarted bool
	for evt, err := range stream.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		if _, ok := evt.(workflow.StartedEvent); ok {
			sawStarted = true
		}
	}
	if !sawStarted {
		t.Fatal("expected StartedEvent even when lockstep has no queued work")
	}
}

// askOnceBinding returns an executor that posts a single ExternalRequest on
// string input and yields the response payload as output once it arrives. It
// drives an input → request-halt → response → output → idle run: exactly two
// input → processing → halt cycles.
func askOnceBinding(id string) workflow.ExecutorBinding {
	port := workflow.RequestPort{
		ID:       "ask",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}
	binding := workflow.ExecutorBinding{ID: id, ImplementationID: "*workflow.Executor"}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,

			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[string]())
				rb.RouteBuilder.
					AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, _ any) (any, error) {
						req, err := workflow.NewExternalRequest("req-1", port, "what is your name?")
						if err != nil {
							return nil, err
						}
						return nil, wctx.PostRequest(req)
					}).
					AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(wctx *workflow.Context, msg any) (any, error) {
						resp := msg.(*workflow.ExternalResponse)
						data, ok := resp.Data.As(port.Response)
						if !ok {
							return nil, fmt.Errorf("response data is not %v", port.Response)
						}
						return nil, wctx.YieldOutput(data)
					})
				return rb, nil
			},
		}, nil
	}
	return binding
}

// respondWhenPending waits until the streaming run has halted with pending
// requests, then sends the response exactly once. In Lockstep mode the run is
// driven synchronously by the WatchStream consumer, so the continuation must be
// injected from a separate goroutine after the halt is observed; sending before
// the halt would let the same cycle absorb the response.
func respondWhenPending(ctx context.Context, stream *inproc.StreamingRun, resp *workflow.ExternalResponse) {
	for {
		if ctx.Err() != nil {
			return
		}
		if status, err := stream.GetStatus(ctx); err == nil && status == inproc.RunStatusPendingRequests {
			_ = stream.SendResponse(ctx, resp)
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStartedEvent_EmittedPerContinuationCycle verifies that a StartedEvent is
// raised for every input → processing → halt cycle, including the continuation
// cycle that runs after an external response is supplied. The streaming
// (OffThread) run loop already emits one StartedEvent per cycle; Lockstep's
// blocking WatchStream drives multiple cycles from a single TakeEventStream
// call and must match that per-cycle semantics.
func TestStartedEvent_EmittedPerContinuationCycle(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  *inproc.ExecutionEnvironment
	}{
		{"lockstep", inproc.Lockstep},
		{"offthread", inproc.OffThread},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asker := askOnceBinding("asker")
			wf, err := workflow.NewBuilder(asker).WithOutputFrom(asker).Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			stream, err := tc.env.RunStreaming(ctx, wf, "kick")
			if err != nil {
				t.Fatalf("RunStreaming: %v", err)
			}
			defer func() { _ = stream.CancelRun() }()

			var startedCount int
			var responded bool
			var gotOutput any
			for evt, err := range stream.WatchStream(ctx) {
				if err != nil {
					t.Fatalf("watch: %v", err)
				}
				switch e := evt.(type) {
				case workflow.StartedEvent:
					startedCount++
				case workflow.RequestInfoEvent:
					if !responded {
						responded = true
						resp, err := e.Request.CreateResponse("Alice")
						if err != nil {
							t.Fatalf("CreateResponse: %v", err)
						}
						go respondWhenPending(ctx, stream, resp)
					}
				case workflow.OutputEvent:
					gotOutput = e.Output
				}
			}

			if got, want := gotOutput, any("Alice"); got != want {
				t.Errorf("output = %v, want %v", got, want)
			}
			if startedCount != 2 {
				t.Errorf("StartedEvent count = %d, want 2 (one per continuation cycle)", startedCount)
			}
		})
	}
}

func TestStreamingRun_WaitToTakeStreamDoesNotBlockOffThread(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, "go")
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	defer func() { _ = stream.Close(ctx) }()

	if err := waitForRunStatus(ctx, stream, inproc.RunStatusIdle); err != nil {
		t.Fatalf("wait for idle before watching stream: %v", err)
	}

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		events = append(events, evt)
	}
	if len(events) == 0 {
		t.Fatal("expected buffered events after waiting to watch stream, got none")
	}
	outputCount := countOutputs(slices.Values(events))
	if outputCount != 1 {
		t.Fatalf("buffered output count = %d, want 1", outputCount)
	}
}

func TestRun_ResumeAcceptsMessages(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "first")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := countOutputs(run.OutgoingEvents()); got != 1 {
		t.Fatalf("initial output count = %d, want 1", got)
	}
	for range run.NewEvents() {
	}

	hadEvents, err := run.Resume(ctx, "second")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !hadEvents {
		t.Fatal("Resume returned hadEvents=false, want true")
	}
	if got := countOutputs(run.NewEvents()); got != 1 {
		t.Fatalf("new output count = %d, want 1", got)
	}
}

func TestRunAndStreamingRun_ProduceEquivalentOutputs(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	nonStreamingOutputs := collectOutputValues(run.OutgoingEvents())
	if err := run.Close(ctx); err != nil {
		t.Fatalf("Close Run: %v", err)
	}

	stream, err := inproc.Default.RunStreaming(ctx, wf, "go")
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	defer func() { _ = stream.Close(ctx) }()
	streamingOutputs := collectStreamingOutputValues(t, ctx, stream)

	if !slices.Equal(nonStreamingOutputs, streamingOutputs) {
		t.Fatalf("streaming outputs = %v, want %v", streamingOutputs, nonStreamingOutputs)
	}
}

func TestStreamingRun_AcceptsSequentialMessages(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	for _, message := range []string{"first", "second"} {
		if err := stream.SendMessage(ctx, message); err != nil {
			t.Fatalf("SendMessage(%q): %v", message, err)
		}
		outputs := collectStreamingOutputValues(t, ctx, stream)
		if !slices.Equal(outputs, []string{"ok"}) {
			t.Fatalf("outputs after %q = %v, want [ok]", message, outputs)
		}
		status, err := stream.GetStatus(ctx)
		if err != nil {
			t.Fatalf("GetStatus after %q: %v", message, err)
		}
		if status != inproc.RunStatusIdle {
			t.Fatalf("status after %q = %v, want Idle", message, status)
		}
	}
}

func TestStreamingRun_SendMessageReturnsErrInvalidInputType(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	err = stream.SendMessage(ctx, 42)
	if !errors.Is(err, workflow.ErrInvalidInputType) {
		t.Fatalf("SendMessage error = %v, want ErrInvalidInputType", err)
	}
}

func TestRunAndStreamingRun_CheckpointableDefaults(t *testing.T) {
	ex := minimalEchoBinding("ex")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertRunCheckpointDefaults(t, run)
	if err := run.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()
	assertStreamingRunCheckpointDefaults(t, stream)
}

func countOutputs(events iter.Seq[workflow.Event]) int {
	var count int
	for evt := range events {
		if _, ok := evt.(workflow.OutputEvent); ok {
			count++
		}
	}
	return count
}

func collectOutputValues(events iter.Seq[workflow.Event]) []string {
	var outputs []string
	for evt := range events {
		if output, ok := evt.(workflow.OutputEvent); ok {
			value, ok := output.Output.(string)
			if ok {
				outputs = append(outputs, value)
			}
		}
	}
	return outputs
}

func collectStreamingOutputValues(t *testing.T, ctx context.Context, stream *inproc.StreamingRun) []string {
	t.Helper()
	var outputs []string
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("WatchStream: %v", err)
		}
		if output, ok := evt.(workflow.OutputEvent); ok {
			value, ok := output.Output.(string)
			if !ok {
				t.Fatalf("OutputEvent.Output = %T, want string", output.Output)
			}
			outputs = append(outputs, value)
		}
	}
	return outputs
}

func sendStreamingMessage(t *testing.T, stream *inproc.StreamingRun, ctx context.Context, message any) {
	t.Helper()
	if err := stream.SendMessage(ctx, message); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
}

func waitForRunStatus(ctx context.Context, run *inproc.StreamingRun, want inproc.RunStatus) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()

	for {
		got, err := run.GetStatus(ctx)
		if err != nil {
			return err
		}
		if got == want {
			return nil
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			got, _ := run.GetStatus(ctx)
			return errors.New("timed out waiting for run status " + runStatusName(want) + ", last status " + runStatusName(got))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func runStatusName(status inproc.RunStatus) string {
	switch status {
	case inproc.RunStatusNotStarted:
		return "NotStarted"
	case inproc.RunStatusIdle:
		return "Idle"
	case inproc.RunStatusPendingRequests:
		return "PendingRequests"
	case inproc.RunStatusEnded:
		return "Ended"
	case inproc.RunStatusRunning:
		return "Running"
	default:
		return "Unknown"
	}
}

func assertRunCheckpointDefaults(t *testing.T, run *inproc.Run) {
	t.Helper()
	if run.IsCheckpointingEnabled() {
		t.Fatal("IsCheckpointingEnabled() = true, want false")
	}
	if got := run.Checkpoints(); len(got) != 0 {
		t.Fatalf("Checkpoints() length = %d, want 0", len(got))
	}
	if checkpoint, ok := run.LastCheckpoint(); ok {
		t.Fatalf("LastCheckpoint() = (%v, true), want false", checkpoint)
	}
}

func assertStreamingRunCheckpointDefaults(t *testing.T, run *inproc.StreamingRun) {
	t.Helper()
	if run.IsCheckpointingEnabled() {
		t.Fatal("IsCheckpointingEnabled() = true, want false")
	}
	if got := run.Checkpoints(); len(got) != 0 {
		t.Fatalf("Checkpoints() length = %d, want 0", len(got))
	}
	if checkpoint, ok := run.LastCheckpoint(); ok {
		t.Fatalf("LastCheckpoint() = (%v, true), want false", checkpoint)
	}
}

func TestSuperStep_CompletedEventPerStep(t *testing.T) {
	starter := &trackingExecutor{id: "Starting", forwardMessages: true}
	receives := &trackingExecutor{id: "Receives", forwardMessages: false}
	uninvoked := &trackingExecutor{id: "Uninvoked", forwardMessages: false}

	startBinding := starter.Bind()
	receivesBinding := receives.Bind()
	uninvokedBinding := uninvoked.Bind()

	wf, err := workflow.NewBuilder(startBinding).
		AddEdge(startBinding, receivesBinding).
		AddEdge(receivesBinding, uninvokedBinding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, "msg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := slices.Collect(run.OutgoingEvents())
	for _, e := range events {
		if errEvt, ok := e.(workflow.ErrorEvent); ok {
			t.Fatalf("workflow produced error event: %v", errEvt.Error)
		}
	}

	completed := 0
	for _, e := range events {
		if _, ok := e.(workflow.SuperStepCompletedEvent); ok {
			completed++
		}
	}
	if completed != 2 {
		t.Errorf("SuperStepCompletedEvent count = %d, want 2", completed)
	}
}

func TestSuperStep_StartedPrecedesCompletedPerStep(t *testing.T) {
	starter := &trackingExecutor{id: "Starting", forwardMessages: true}
	receives := &trackingExecutor{id: "Receives", forwardMessages: false}

	startBinding := starter.Bind()
	receivesBinding := receives.Bind()

	wf, err := workflow.NewBuilder(startBinding).
		AddEdge(startBinding, receivesBinding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, "msg")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	depth := 0
	maxDepth := 0
	pairs := 0
	for evt := range run.OutgoingEvents() {
		switch evt.(type) {
		case workflow.SuperStepStartedEvent:
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case workflow.SuperStepCompletedEvent:
			if depth == 0 {
				t.Errorf("SuperStepCompletedEvent without preceding SuperStepStartedEvent")
			} else {
				depth--
				pairs++
			}
		}
	}
	if depth != 0 {
		t.Errorf("unbalanced started/completed events: %d unfinished", depth)
	}
	if maxDepth != 1 {
		t.Errorf("max nesting depth = %d, want 1 (sequential supersteps)", maxDepth)
	}
	if pairs != 2 {
		t.Errorf("started/completed pair count = %d, want 2", pairs)
	}
}

type trackingExecutor struct {
	id              string
	forwardMessages bool

	deliveryStarting atomic.Int64
	deliveryFinished atomic.Int64

	mu       sync.Mutex
	received []string
}

func (te *trackingExecutor) Bind() workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               te.id,
		ImplementationID: "*trackingExecutor",
		RawValue:         te,
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: te.id,
			OnMessageDeliveryStartingFunc: func(_ *workflow.Context) error {
				te.deliveryStarting.Add(1)
				return nil
			},
			OnMessageDeliveryFinishedFunc: func(_ *workflow.Context) error {
				te.deliveryFinished.Add(1)
				return nil
			},
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					s := msg.(string)
					te.mu.Lock()
					te.received = append(te.received, s)
					te.mu.Unlock()
					if te.forwardMessages {
						return nil, ctx.SendMessage("", s)
					}
					return nil, nil
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}

func TestDeliveryEvents_InvokedOncePerExecutorPerSuperstep(t *testing.T) {
	starter := &trackingExecutor{id: "Starting", forwardMessages: true}
	receives := &trackingExecutor{id: "Receives", forwardMessages: false}
	uninvoked := &trackingExecutor{id: "Uninvoked", forwardMessages: false}

	startBinding := starter.Bind()
	receivesBinding := receives.Bind()
	uninvokedBinding := uninvoked.Bind()

	wf, err := workflow.NewBuilder(startBinding).
		AddEdge(startBinding, receivesBinding).
		AddEdge(receivesBinding, uninvokedBinding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := inproc.Default.Run(context.Background(), wf, "msg"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := starter.deliveryStarting.Load(); got != 1 {
		t.Errorf("starter.deliveryStarting = %d, want 1", got)
	}
	if got := starter.deliveryFinished.Load(); got != 1 {
		t.Errorf("starter.deliveryFinished = %d, want 1", got)
	}
	if got := receives.deliveryStarting.Load(); got != 1 {
		t.Errorf("receives.deliveryStarting = %d, want 1", got)
	}
	if got := receives.deliveryFinished.Load(); got != 1 {
		t.Errorf("receives.deliveryFinished = %d, want 1", got)
	}
	if got := uninvoked.deliveryStarting.Load(); got != 0 {
		t.Errorf("uninvoked.deliveryStarting = %d, want 0", got)
	}
	if got := uninvoked.deliveryFinished.Load(); got != 0 {
		t.Errorf("uninvoked.deliveryFinished = %d, want 0", got)
	}
}

func TestDeliveryEvents_FinishedRunsEvenWhenHandlerErrors(t *testing.T) {
	finishedCalls := atomic.Int64{}
	id := "boom"
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			OnMessageDeliveryFinishedFunc: func(_ *workflow.Context) error {
				finishedCalls.Add(1)
				return nil
			},
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(_ *workflow.Context, _ any) (any, error) {
					return nil, errBoom
				})
				return rb, nil
			},
		}, nil
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "x"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := finishedCalls.Load(); got != 1 {
		t.Errorf("OnMessageDeliveryFinished called %d times, want 1", got)
	}
}

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }
