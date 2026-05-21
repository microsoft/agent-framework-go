// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func TestCheckpoint_ResumeWithPendingRequests_RepublishesRequestInfoEvents(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			ctx := context.Background()
			wf, _ := createCheckpointRequestWorkflow(t)
			manager := checkpoint.NewInMemoryManager()

			first, err := env.env.WithCheckpointing(manager).Run(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			originalRequests := collectRequests(first.OutgoingEvents())
			if len(originalRequests) == 0 {
				t.Fatal("expected at least one original RequestInfoEvent")
			}
			checkpointInfo, ok := first.LastCheckpoint()
			if !ok {
				t.Fatal("expected checkpoint")
			}
			if err := first.Close(ctx); err != nil {
				t.Fatalf("Close first run: %v", err)
			}

			resumed, err := env.env.WithCheckpointing(manager).Resume(ctx, wf, checkpointInfo)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}

			replayedRequests := collectRequests(resumed.NewEvents())
			if len(replayedRequests) != len(originalRequests) {
				t.Fatalf("replayed request count = %d, want %d", len(replayedRequests), len(originalRequests))
			}
			originalIDs := requestIDs(originalRequests)
			replayedIDs := requestIDs(replayedRequests)
			slices.Sort(originalIDs)
			slices.Sort(replayedIDs)
			if !slices.Equal(replayedIDs, originalIDs) {
				t.Fatalf("replayed request IDs = %v, want %v", replayedIDs, originalIDs)
			}
		})
	}
}

func TestCheckpoint_ResumeWithPendingRequests_RunStatusIsPendingRequests(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			ctx := context.Background()
			wf, _ := createCheckpointRequestWorkflow(t)
			manager := checkpoint.NewInMemoryManager()

			first, err := env.env.WithCheckpointing(manager).Run(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			checkpointInfo, ok := first.LastCheckpoint()
			if !ok {
				t.Fatal("expected checkpoint")
			}
			if err := first.Close(ctx); err != nil {
				t.Fatalf("Close first run: %v", err)
			}

			resumed, err := env.env.WithCheckpointing(manager).Resume(ctx, wf, checkpointInfo)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			status, err := resumed.GetStatus(ctx)
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if status != inproc.RunStatusPendingRequests {
				t.Fatalf("status = %v, want PendingRequests", status)
			}
		})
	}
}

func TestCheckpoint_ResumeWithRepublishDisabled_DoesNotEmitRequestInfoEvents(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			ctx := context.Background()
			wf, _ := createCheckpointRequestWorkflow(t)
			manager := checkpoint.NewInMemoryManager()

			first, err := env.env.WithCheckpointing(manager).Run(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(collectRequests(first.OutgoingEvents())) == 0 {
				t.Fatal("expected original RequestInfoEvent")
			}
			checkpointInfo, ok := first.LastCheckpoint()
			if !ok {
				t.Fatal("expected checkpoint")
			}
			if err := first.Close(ctx); err != nil {
				t.Fatalf("Close first run: %v", err)
			}

			resumed, err := env.env.WithCheckpointing(manager).Resume(ctx, wf, checkpointInfo, inproc.WithPendingRequestRepublish(false))
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if len(collectRequests(resumed.NewEvents())) != 0 {
				t.Fatal("did not expect RequestInfoEvent when pending request republish is disabled")
			}
			status, err := resumed.GetStatus(ctx)
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if status != inproc.RunStatusPendingRequests {
				t.Fatalf("status = %v, want PendingRequests", status)
			}
		})
	}
}

func TestCheckpoint_ResumeIgnoresSessionIDOption(t *testing.T) {
	ctx := context.Background()
	wf, _ := createCheckpointRequestWorkflow(t)
	manager := checkpoint.NewInMemoryManager()
	const checkpointSession = "checkpoint-session"

	first, err := inproc.Default.WithCheckpointing(manager).Run(ctx, wf, "Hello", inproc.WithSessionID(checkpointSession))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	checkpointInfo, ok := first.LastCheckpoint()
	if !ok {
		t.Fatal("expected checkpoint")
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("Close first run: %v", err)
	}

	resumed, err := inproc.Default.WithCheckpointing(manager).Resume(ctx, wf, checkpointInfo, inproc.WithSessionID("ignored-session"))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := resumed.SessionID(); got != checkpointSession {
		t.Fatalf("resumed session ID = %q, want %q", got, checkpointSession)
	}
}

func TestCheckpoint_ResumeRespondToPendingRequest_CompletesWithoutDuplicate(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			ctx := context.Background()
			wf, received := createCheckpointRequestWorkflow(t)
			manager := checkpoint.NewInMemoryManager()

			first, err := env.env.WithCheckpointing(manager).Run(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			pendingRequest := firstRequest(t, first.OutgoingEvents())
			checkpointInfo, ok := first.LastCheckpoint()
			if !ok {
				t.Fatal("expected checkpoint")
			}
			if err := first.Close(ctx); err != nil {
				t.Fatalf("Close first run: %v", err)
			}

			resumed, err := env.env.WithCheckpointing(manager).Resume(ctx, wf, checkpointInfo)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			requestEventCount := 0
			for evt := range resumed.NewEvents() {
				if reqEvt, ok := evt.(workflow.RequestInfoEvent); ok {
					requestEventCount++
					if reqEvt.Request.RequestID != pendingRequest.RequestID {
						t.Fatalf("replayed request ID = %q, want %q", reqEvt.Request.RequestID, pendingRequest.RequestID)
					}
				}
			}
			if requestEventCount != 1 {
				t.Fatalf("request event count = %d, want 1", requestEventCount)
			}
			status, err := resumed.GetStatus(ctx)
			if err != nil {
				t.Fatalf("GetStatus before response: %v", err)
			}
			if status != inproc.RunStatusPendingRequests {
				t.Fatalf("status before response = %v, want PendingRequests", status)
			}

			response, err := pendingRequest.NewResponse("World")
			if err != nil {
				t.Fatalf("NewResponse: %v", err)
			}
			if _, err := resumed.Resume(ctx, response); err != nil {
				t.Fatalf("Resume with response: %v", err)
			}
			postResponseEvents := collectEvents(resumed.NewEvents())
			if hasEventType[workflow.RequestInfoEvent](postResponseEvents) {
				t.Fatal("did not expect duplicate RequestInfoEvent after response")
			}
			if hasErrorEvents(postResponseEvents) {
				t.Fatalf("unexpected error events after response: %#v", postResponseEvents)
			}
			if got := received.Load(); got != 1 {
				t.Fatalf("sink receive count = %d, want 1", got)
			}
			status, err = resumed.GetStatus(ctx)
			if err != nil {
				t.Fatalf("GetStatus after response: %v", err)
			}
			if status != inproc.RunStatusIdle {
				t.Fatalf("status after response = %v, want Idle", status)
			}
		})
	}
}

func TestCheckpoint_RestoreWithPendingRequests_RepublishesRequestInfoEvents(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			ctx := context.Background()
			wf, received := createCheckpointRequestWorkflow(t)
			manager := checkpoint.NewInMemoryManager()

			run, err := env.env.WithCheckpointing(manager).Run(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			pendingRequest := firstRequest(t, run.OutgoingEvents())
			checkpointInfo, ok := run.LastCheckpoint()
			if !ok {
				t.Fatal("expected checkpoint")
			}

			response, err := pendingRequest.NewResponse("World")
			if err != nil {
				t.Fatalf("NewResponse: %v", err)
			}
			if _, err := run.Resume(ctx, response); err != nil {
				t.Fatalf("Resume with first response: %v", err)
			}
			if got := received.Load(); got != 1 {
				t.Fatalf("sink receive count after first response = %d, want 1", got)
			}
			if err := run.RestoreCheckpoint(ctx, checkpointInfo); err != nil {
				t.Fatalf("RestoreCheckpoint: %v", err)
			}

			replayedRequests := collectRequests(run.NewEvents())
			if len(replayedRequests) != 1 {
				t.Fatalf("replayed request count = %d, want 1", len(replayedRequests))
			}
			if replayedRequests[0].RequestID != pendingRequest.RequestID {
				t.Fatalf("replayed request ID = %q, want %q", replayedRequests[0].RequestID, pendingRequest.RequestID)
			}

			response, err = replayedRequests[0].NewResponse("Again")
			if err != nil {
				t.Fatalf("NewResponse after restore: %v", err)
			}
			if _, err := run.Resume(ctx, response); err != nil {
				t.Fatalf("Resume with restored response: %v", err)
			}
			postResponseEvents := collectEvents(run.NewEvents())
			if hasErrorEvents(postResponseEvents) {
				t.Fatalf("unexpected error events after restored response: %#v", postResponseEvents)
			}
			if got := received.Load(); got != 2 {
				t.Fatalf("sink receive count after restored response = %d, want 2", got)
			}
			status, err := run.GetStatus(ctx)
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if status != inproc.RunStatusIdle {
				t.Fatalf("status after restored response = %v, want Idle", status)
			}
		})
	}
}

func TestCheckpoint_RestoreClearsQueuedExternalResponsesBeforeImport(t *testing.T) {
	ctx := context.Background()
	wf, received := createCheckpointRequestWorkflow(t)
	manager := checkpoint.NewInMemoryManager()

	stream, err := inproc.Lockstep.WithCheckpointing(manager).RunStreaming(ctx, wf, "Hello")
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	pendingRequest, checkpointInfo := capturePendingRequestAndCheckpointFromStream(t, ctx, stream)

	response, err := pendingRequest.NewResponse("World")
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	if err := stream.SendResponse(ctx, response); err != nil {
		t.Fatalf("SendResponse before restore: %v", err)
	}
	if err := stream.RestoreCheckpoint(ctx, checkpointInfo); err != nil {
		t.Fatalf("RestoreCheckpoint: %v", err)
	}

	restoredEvents := readStreamToHalt(t, ctx, stream)
	replayedRequests := requestsFromEvents(restoredEvents)
	if len(replayedRequests) != 1 {
		t.Fatalf("replayed request count = %d, want 1", len(replayedRequests))
	}
	if replayedRequests[0].RequestID != pendingRequest.RequestID {
		t.Fatalf("replayed request ID = %q, want %q", replayedRequests[0].RequestID, pendingRequest.RequestID)
	}
	if hasErrorEvents(restoredEvents) {
		t.Fatalf("unexpected error events after restore: %#v", restoredEvents)
	}
	if got := received.Load(); got != 0 {
		t.Fatalf("queued stale response was processed %d times, want 0", got)
	}
	status, err := stream.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus after restore: %v", err)
	}
	if status != inproc.RunStatusPendingRequests {
		t.Fatalf("status after restore = %v, want PendingRequests", status)
	}

	response, err = replayedRequests[0].NewResponse("Again")
	if err != nil {
		t.Fatalf("NewResponse after restore: %v", err)
	}
	if err := stream.SendResponse(ctx, response); err != nil {
		t.Fatalf("SendResponse after restore: %v", err)
	}
	completionEvents := readStreamToHalt(t, ctx, stream)
	if hasErrorEvents(completionEvents) {
		t.Fatalf("unexpected error events after fresh response: %#v", completionEvents)
	}
	if got := received.Load(); got != 1 {
		t.Fatalf("fresh response receive count = %d, want 1", got)
	}
	status, err = stream.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus final: %v", err)
	}
	if status != inproc.RunStatusIdle {
		t.Fatalf("final status = %v, want Idle", status)
	}
}

func TestCheckpoint_RestoreClearsExecutorInstancesBeforeImport(t *testing.T) {
	ctx := context.Background()
	manager := checkpoint.NewInMemoryManager()
	var nextInstanceID int64
	binding := workflow.ExecutorBinding{
		ID:           "counter",
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			instanceID := atomic.AddInt64(&nextInstanceID, 1)
			count := 0
			return &workflow.Executor{
				ID: "counter",
				Spec: workflow.ExecutorSpec{
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.YieldsOutputType(reflect.TypeFor[struct {
							InstanceID int64
							Count      int
						}]())
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, _ any) (any, error) {
							count++
							return nil, ctx.YieldOutput(struct {
								InstanceID int64
								Count      int
							}{InstanceID: instanceID, Count: count})
						})
						return rb, nil
					},
				},
			}, nil
		},
	}
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.WithCheckpointing(manager).Run(ctx, wf, "first")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	checkpointInfo, ok := run.LastCheckpoint()
	if !ok {
		t.Fatal("expected checkpoint")
	}
	if _, err := run.Resume(ctx, "second"); err != nil {
		t.Fatalf("Resume second: %v", err)
	}
	for range run.NewEvents() {
	}

	if err := run.RestoreCheckpoint(ctx, checkpointInfo); err != nil {
		t.Fatalf("RestoreCheckpoint: %v", err)
	}
	if _, err := run.Resume(ctx, "third"); err != nil {
		t.Fatalf("Resume third: %v", err)
	}

	var got struct {
		InstanceID int64
		Count      int
	}
	for evt := range run.NewEvents() {
		if out, ok := evt.(workflow.OutputEvent); ok {
			got = out.Output.(struct {
				InstanceID int64
				Count      int
			})
		}
	}
	if got.InstanceID == 1 {
		t.Fatalf("restored run reused stale executor instance: %+v", got)
	}
	if got.Count != 1 {
		t.Fatalf("restored executor count = %d, want 1", got.Count)
	}
}

func TestCheckpoint_FirstCheckpointHasNoParent(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			ctx := context.Background()
			store, manager := newFileSystemJSONCheckpointManager(t)
			wf := createCheckpointChainWorkflow(t, "a", "b")

			run, err := env.env.WithCheckpointing(manager).Run(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			checkpoints := run.Checkpoints()
			if len(checkpoints) == 0 {
				t.Fatal("expected at least one checkpoint")
			}

			var zero workflow.CheckpointInfo
			children, err := store.RetrieveIndex(ctx, checkpoints[0].SessionID, &zero)
			if err != nil {
				t.Fatalf("RetrieveIndex: %v", err)
			}
			if len(children) != 0 {
				t.Fatalf("first checkpoint was indexed with zero parent; children of zero parent = %+v", children)
			}
		})
	}
}

func TestCheckpoint_SubsequentCheckpointsChainParents(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			ctx := context.Background()
			store, manager := newFileSystemJSONCheckpointManager(t)
			wf := createCheckpointChainWorkflow(t, "a", "b", "c")

			run, err := env.env.WithCheckpointing(manager).Run(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			checkpoints := run.Checkpoints()
			if len(checkpoints) < 3 {
				t.Fatalf("checkpoint count = %d, want at least 3", len(checkpoints))
			}

			for i := 1; i < 3; i++ {
				children, err := store.RetrieveIndex(ctx, checkpoints[i].SessionID, &checkpoints[i-1])
				if err != nil {
					t.Fatalf("RetrieveIndex parent %d: %v", i-1, err)
				}
				if !slices.Contains(children, checkpoints[i]) {
					t.Fatalf("children of checkpoint %d = %+v, want checkpoint %d %+v", i-1, children, i, checkpoints[i])
				}
			}
		})
	}
}

func TestCheckpoint_AfterResumeUsesResumedCheckpointAsParent(t *testing.T) {
	for _, env := range checkpointTestEnvironments() {
		t.Run(env.name, func(t *testing.T) {
			ctx := context.Background()
			store, manager := newFileSystemJSONCheckpointManager(t)
			wf, _ := createCheckpointRequestWorkflow(t)

			first, err := env.env.WithCheckpointing(manager).Run(ctx, wf, "Hello")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			pendingRequest := firstRequest(t, first.OutgoingEvents())
			resumePoint, ok := first.LastCheckpoint()
			if !ok {
				t.Fatal("expected checkpoint")
			}
			if err := first.Close(ctx); err != nil {
				t.Fatalf("Close first run: %v", err)
			}

			resumed, err := env.env.WithCheckpointing(manager).Resume(ctx, wf, resumePoint)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			response, err := pendingRequest.NewResponse("World")
			if err != nil {
				t.Fatalf("NewResponse: %v", err)
			}
			if _, err := resumed.Resume(ctx, response); err != nil {
				t.Fatalf("Resume with response: %v", err)
			}
			var firstResumedCheckpoint workflow.CheckpointInfo
			for evt := range resumed.NewEvents() {
				stepEvt, ok := evt.(workflow.SuperStepCompletedEvent)
				if !ok || stepEvt.CompletionInfo == nil || stepEvt.CompletionInfo.CheckpointInfo == nil {
					continue
				}
				checkpointInfo := *stepEvt.CompletionInfo.CheckpointInfo
				if checkpointInfo != resumePoint {
					firstResumedCheckpoint = checkpointInfo
					break
				}
			}
			if firstResumedCheckpoint == (workflow.CheckpointInfo{}) {
				t.Fatal("expected checkpoint after resume")
			}

			children, err := store.RetrieveIndex(ctx, resumePoint.SessionID, &resumePoint)
			if err != nil {
				t.Fatalf("RetrieveIndex: %v", err)
			}
			if !slices.Contains(children, firstResumedCheckpoint) {
				t.Fatalf("children of resume point = %+v, want first resumed checkpoint %+v", children, firstResumedCheckpoint)
			}
		})
	}
}

func TestCheckpoint_ExecutorCheckpointHooks(t *testing.T) {
	for _, useCheckpointing := range []bool{true, false} {
		t.Run(map[bool]string{true: "checkpointing", false: "no_checkpointing"}[useCheckpointing], func(t *testing.T) {
			ctx := context.Background()
			fixture := newCheckpointHookFixture()
			env := inproc.OffThread
			manager := checkpoint.NewInMemoryManager()
			var run *inproc.Run
			var err error
			if useCheckpointing {
				run, err = env.WithCheckpointing(manager).Run(ctx, fixture.workflow, "Message")
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
				if len(run.Checkpoints()) != checkpointHookStepsPerInputBatch {
					t.Fatalf("checkpoint count = %d, want %d", len(run.Checkpoints()), checkpointHookStepsPerInputBatch)
				}
			} else {
				run, err = env.Run(ctx, fixture.workflow, "Message")
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
			}
			events := collectEvents(run.OutgoingEvents())
			if hasErrorEvents(events) {
				t.Fatalf("unexpected error events: %#v", events)
			}

			expected := int64(0)
			if useCheckpointing {
				expected = checkpointHookStepsPerInputBatch
			}
			assertHookCounts(t, fixture.starting, expected, 0)
			assertHookCounts(t, fixture.receives, expected, 0)
			assertHookCounts(t, fixture.uninvoked, 0, 0)
		})
	}
}

func TestCheckpoint_ExecutorRestoreHooks(t *testing.T) {
	for _, restoreCheckpoint := range []bool{true, false} {
		t.Run(map[bool]string{true: "restore", false: "no_restore"}[restoreCheckpoint], func(t *testing.T) {
			ctx := context.Background()
			manager := checkpoint.NewInMemoryManager()
			runFixture := newCheckpointHookFixture()
			run, err := inproc.OffThread.WithCheckpointing(manager).Run(ctx, runFixture.workflow, "Message")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(run.Checkpoints()) != checkpointHookStepsPerInputBatch {
				t.Fatalf("checkpoint count = %d, want %d", len(run.Checkpoints()), checkpointHookStepsPerInputBatch)
			}

			validateFixture := runFixture
			expectedCheckpoints := int64(checkpointHookStepsPerInputBatch)
			if restoreCheckpoint {
				firstCheckpoint := run.Checkpoints()[0]
				if err := run.Close(ctx); err != nil {
					t.Fatalf("Close run: %v", err)
				}
				validateFixture = newCheckpointHookFixture()
				resumed, err := inproc.OffThread.WithCheckpointing(manager).Resume(ctx, validateFixture.workflow, firstCheckpoint)
				if err != nil {
					t.Fatalf("Resume: %v", err)
				}
				events := collectEvents(resumed.OutgoingEvents())
				if hasErrorEvents(events) {
					t.Fatalf("unexpected resumed error events: %#v", events)
				}
				expectedCheckpoints--
			}

			expectedRestoreCalls := int64(0)
			if restoreCheckpoint {
				expectedRestoreCalls = 1
			}
			assertHookCounts(t, validateFixture.starting, expectedCheckpoints, expectedRestoreCalls)
			assertHookCounts(t, validateFixture.receives, expectedCheckpoints, expectedRestoreCalls)
			assertHookCounts(t, validateFixture.uninvoked, 0, 0)
		})
	}
}

func createCheckpointRequestWorkflow(t *testing.T) (*workflow.Workflow, *atomic.Int64) {
	t.Helper()
	port := workflow.RequestPort{
		ID:       "TestPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}
	portBinding := workflow.BindRequestPort(port)

	received := &atomic.Int64{}
	sinkBinding := workflow.ExecutorBinding{
		ID:           "Processor",
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "Processor",
				Spec: workflow.ExecutorSpec{
					DisableAutoSendMessageHandlerResultObject: true,
					DisableAutoYieldOutputHandlerResultObject: true,
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.YieldsOutputType(reflect.TypeFor[string]())
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
							received.Add(1)
							return nil, ctx.YieldOutput(msg)
						})
						return rb, nil
					},
				},
			}, nil
		},
	}

	wf, err := workflow.NewBuilder(portBinding).
		AddEdge(portBinding, sinkBinding).
		WithOutputFrom(sinkBinding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return wf, received
}

func newFileSystemJSONCheckpointManager(t *testing.T) (*checkpoint.FileSystemJSONStore, checkpoint.Manager) {
	t.Helper()
	store, err := checkpoint.NewFileSystemJSONStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileSystemJSONStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close checkpoint store: %v", err)
		}
	})
	return store, checkpoint.NewJSONManager(store)
}

func createCheckpointChainWorkflow(t *testing.T, ids ...string) *workflow.Workflow {
	t.Helper()
	if len(ids) == 0 {
		t.Fatal("expected at least one executor ID")
	}

	bindings := make([]workflow.ExecutorBinding, 0, len(ids))
	for _, id := range ids {
		id := id
		bindings = append(bindings, workflow.ExecutorBinding{
			ID:           id,
			ExecutorType: reflect.TypeFor[*workflow.Executor](),
			NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
				return &workflow.Executor{
					ID: id,
					Spec: workflow.ExecutorSpec{
						DisableAutoSendMessageHandlerResultObject: true,
						DisableAutoYieldOutputHandlerResultObject: true,
						ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
							rb.SendsMessageType(reflect.TypeFor[string]())
							rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
								return nil, ctx.SendMessage("", msg)
							})
							return rb, nil
						},
					},
				}, nil
			},
		})
	}

	builder := workflow.NewBuilder(bindings[0])
	for i := 1; i < len(bindings); i++ {
		builder = builder.AddEdge(bindings[i-1], bindings[i])
	}
	wf, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return wf
}

type checkpointTestEnvironment struct {
	name string
	env  *inproc.ExecutionEnvironment
}

func checkpointTestEnvironments() []checkpointTestEnvironment {
	return []checkpointTestEnvironment{
		{name: "off_thread", env: inproc.OffThread},
		{name: "lockstep", env: inproc.Lockstep},
	}
}

func firstRequest(t *testing.T, events func(func(workflow.Event) bool)) *workflow.ExternalRequest {
	t.Helper()
	for evt := range events {
		if reqEvt, ok := evt.(workflow.RequestInfoEvent); ok {
			return reqEvt.Request
		}
	}
	t.Fatal("expected RequestInfoEvent")
	return nil
}

func collectRequests(events func(func(workflow.Event) bool)) []*workflow.ExternalRequest {
	var requests []*workflow.ExternalRequest
	for evt := range events {
		if reqEvt, ok := evt.(workflow.RequestInfoEvent); ok {
			requests = append(requests, reqEvt.Request)
		}
	}
	return requests
}

func requestsFromEvents(events []workflow.Event) []*workflow.ExternalRequest {
	var requests []*workflow.ExternalRequest
	for _, evt := range events {
		if reqEvt, ok := evt.(workflow.RequestInfoEvent); ok {
			requests = append(requests, reqEvt.Request)
		}
	}
	return requests
}

func requestIDs(requests []*workflow.ExternalRequest) []string {
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.RequestID)
	}
	return ids
}

func collectEvents(events func(func(workflow.Event) bool)) []workflow.Event {
	var result []workflow.Event
	for evt := range events {
		result = append(result, evt)
	}
	return result
}

func readStreamToHalt(t *testing.T, ctx context.Context, run *inproc.StreamingRun) []workflow.Event {
	t.Helper()
	var events []workflow.Event
	for evt, err := range run.WatchUntilHalt(ctx) {
		if err != nil {
			t.Fatalf("WatchUntilHalt: %v", err)
		}
		events = append(events, evt)
	}
	return events
}

func capturePendingRequestAndCheckpointFromStream(t *testing.T, ctx context.Context, run *inproc.StreamingRun) (*workflow.ExternalRequest, workflow.CheckpointInfo) {
	t.Helper()
	var pendingRequest *workflow.ExternalRequest
	var checkpointInfo workflow.CheckpointInfo
	for _, evt := range readStreamToHalt(t, ctx, run) {
		if reqEvt, ok := evt.(workflow.RequestInfoEvent); ok && pendingRequest == nil {
			pendingRequest = reqEvt.Request
		}
		if stepEvt, ok := evt.(workflow.SuperStepCompletedEvent); ok && stepEvt.CompletionInfo != nil {
			if stepEvt.CompletionInfo.CheckpointInfo != nil {
				checkpointInfo = *stepEvt.CompletionInfo.CheckpointInfo
			}
		}
	}
	if pendingRequest == nil {
		t.Fatal("expected pending request")
	}
	if checkpointInfo == (workflow.CheckpointInfo{}) {
		t.Fatal("expected checkpoint")
	}
	return pendingRequest, checkpointInfo
}

func hasErrorEvents(events []workflow.Event) bool {
	for _, evt := range events {
		switch evt.(type) {
		case workflow.ErrorEvent, workflow.ExecutorFailedEvent:
			return true
		}
	}
	return false
}

func hasEventType[T workflow.Event](events []workflow.Event) bool {
	for _, evt := range events {
		if _, ok := evt.(T); ok {
			return true
		}
	}
	return false
}

const checkpointHookStepsPerInputBatch = 2

type checkpointHookExecutor struct {
	id              string
	forwardMessages bool

	checkpointingCalls     atomic.Int64
	checkpointRestoredCall atomic.Int64
}

func (e *checkpointHookExecutor) Bind() workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:           e.id,
		ExecutorType: reflect.TypeFor[*checkpointHookExecutor](),
		RawValue:     e,
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: e.id,
				Spec: workflow.ExecutorSpec{
					DisableAutoSendMessageHandlerResultObject: true,
					DisableAutoYieldOutputHandlerResultObject: true,
					OnCheckpoint: func(_ *workflow.Context) error {
						e.checkpointingCalls.Add(1)
						return nil
					},
					OnCheckpointRestored: func(_ *workflow.Context) error {
						e.checkpointRestoredCall.Add(1)
						return nil
					},
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.SendsMessageType(reflect.TypeFor[string]())
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
							if e.forwardMessages {
								return nil, ctx.SendMessage("", msg)
							}
							return nil, nil
						})
						return rb, nil
					},
				},
			}, nil
		},
	}
	return binding
}

type checkpointHookFixture struct {
	starting  *checkpointHookExecutor
	receives  *checkpointHookExecutor
	uninvoked *checkpointHookExecutor
	workflow  *workflow.Workflow
}

func newCheckpointHookFixture() *checkpointHookFixture {
	fixture := &checkpointHookFixture{
		starting:  &checkpointHookExecutor{id: "Starting", forwardMessages: true},
		receives:  &checkpointHookExecutor{id: "Receives", forwardMessages: false},
		uninvoked: &checkpointHookExecutor{id: "Uninvoked", forwardMessages: false},
	}
	startBinding := fixture.starting.Bind()
	receivesBinding := fixture.receives.Bind()
	uninvokedBinding := fixture.uninvoked.Bind()
	wf, err := workflow.NewBuilder(startBinding).
		AddEdge(startBinding, receivesBinding).
		AddEdge(receivesBinding, uninvokedBinding).
		Build()
	if err != nil {
		panic(err)
	}
	fixture.workflow = wf
	return fixture
}

func assertHookCounts(t *testing.T, executor *checkpointHookExecutor, checkpointingCalls int64, restoredCalls int64) {
	t.Helper()
	if got := executor.checkpointingCalls.Load(); got != checkpointingCalls {
		t.Fatalf("%s checkpointing calls = %d, want %d", executor.id, got, checkpointingCalls)
	}
	if got := executor.checkpointRestoredCall.Load(); got != restoredCalls {
		t.Fatalf("%s restored calls = %d, want %d", executor.id, got, restoredCalls)
	}
}
