// Copyright (c) Microsoft. All rights reserved.

package a2aprovider_test

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/push"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

// These two factory tests directly port AgentRunModeTests at
// 0c9944cc9f577d51277ac7c55dbc388b60a577af.
func TestAgentRunModeReturnTaskWhenNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("ReturnTaskWhen(nil) did not panic")
		}
	}()
	a2aprovider.ReturnTaskWhen(nil)
}

func TestAgentRunModeString(t *testing.T) {
	if got := a2aprovider.ReturnMessage().String(); got != "message" {
		t.Errorf("ReturnMessage().String() = %q, want message", got)
	}
	if got := a2aprovider.ReturnTask().String(); got != "task" {
		t.Errorf("ReturnTask().String() = %q, want task", got)
	}
	if got := a2aprovider.ReturnTaskWhen(func(context.Context, *a2asrv.ExecutorContext) (bool, error) {
		return true, nil
	}).String(); got != "dynamic" {
		t.Errorf("ReturnTaskWhen(callback).String() = %q, want dynamic", got)
	}
}

// These fixtures port the response-mode cases in A2AAgentHandlerTests at
// 0c9944cc9f577d51277ac7c55dbc388b60a577af. Native handler integration checks
// are separate from the direct executor golden tests.
func responseModeAgent(updates []*agent.ResponseUpdate, finalErr error) *agent.Agent {
	return newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			stream, _ := agent.GetOption(options, agent.Stream)
			if !stream {
				yield(nil, errors.New("streaming agent execution is required"))
				return
			}
			for _, update := range updates {
				if !yield(update, nil) {
					return
				}
			}
			if finalErr != nil {
				yield(nil, finalErr)
			}
		}
	})
}

func responseModeExecutor(updates []*agent.ResponseUpdate, finalErr error, cfg a2aprovider.ExecutorConfig) a2asrv.AgentExecutor {
	return a2aprovider.NewExecutor(responseModeAgent(updates, finalErr), cfg)
}

func responseModeTasks(events []a2a.Event) []*a2a.Task {
	var tasks []*a2a.Task
	for _, event := range events {
		if task, ok := event.(*a2a.Task); ok {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func assertResponseModeSubmitted(t *testing.T, events []a2a.Event) {
	t.Helper()
	tasks := responseModeTasks(events)
	if len(tasks) != 1 || tasks[0].Status.State != a2a.TaskStateSubmitted {
		t.Fatalf("tasks = %#v, want one submitted task", tasks)
	}
	for _, event := range events {
		if _, ok := event.(*a2a.Message); ok {
			t.Fatal("unexpected message event")
		}
	}
}

func TestResponseMode_DefaultMessage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		native    bool
		streaming bool
		immediate bool
	}{
		{name: "direct/send"},
		{name: "direct/stream", streaming: true},
		{name: "native/send/wait", native: true},
		{name: "native/send/immediate", native: true, immediate: true},
		{name: "native/stream/wait", native: true, streaming: true},
		{name: "native/stream/immediate", native: true, streaming: true, immediate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := agenttest.NewContinuationToken(t, "inner-token")
			updates := []*agent.ResponseUpdate{
				hostedUpdate("m1", "chunk 1"),
				{ContinuationToken: token},
				hostedUpdate("m1", "chunk 2"),
				{ResponseID: "final-response", ContinuationToken: token},
			}
			var calls atomic.Int32
			a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					if calls.Add(1) != 1 {
						yield(nil, errors.New("unexpected additional agent call"))
						return
					}
					if stream, _ := agent.GetOption(options, agent.Stream); !stream {
						yield(nil, errors.New("streaming agent execution is required"))
						return
					}
					for _, update := range updates {
						if !yield(update, nil) {
							return
						}
					}
				}
			})
			request := hostedExecutorContext("", "ctx")
			request.Message.ContextID = "ctx"
			var events []a2a.Event
			var err error
			if tc.native {
				h := a2aprovider.NewHandler(a, a2aprovider.ExecutorConfig{})
				send := &a2a.SendMessageRequest{Message: request.Message, Config: &a2a.SendMessageConfig{ReturnImmediately: tc.immediate}}
				if tc.streaming {
					events, err = collectStreamingEventsAndError(h.SendStreamingMessage(t.Context(), send))
				} else {
					result, sendErr := h.SendMessage(t.Context(), send)
					err = sendErr
					if result != nil {
						events = append(events, result)
					}
				}
			} else {
				e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{})
				seq := e.Execute(t.Context(), request)
				if tc.streaming {
					seq = directExecutorStream(t.Context(), e, request)
				}
				events, err = collectStreamingEventsAndError(seq)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("agent calls = %d, want 1", got)
			}
			if err != nil {
				t.Fatalf("error = %v, want one aggregated message without continuation polling", err)
			}
			if len(events) != 1 || len(responseModeTasks(events)) != 0 {
				t.Fatalf("events = %#v, want exactly one message and no task events", events)
			}
			msg := singleHostedMessage(t, events)
			if msg.ID != "final-response" || msg.ContextID != "ctx" || msg.TaskID != "" || msg.Role != a2a.MessageRoleAgent || msg.Metadata != nil {
				t.Fatalf("message = %#v, want final-response in ctx without task ID or metadata", msg)
			}
			if len(msg.Parts) != 1 || msg.Parts[0].Text() != "chunk 1chunk 2" {
				t.Fatalf("parts = %#v, want one aggregated text part", msg.Parts)
			}
		})
	}
}

func TestResponseMode_DynamicMessage(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Test response"), a2aprovider.ExecutorConfig{
		AgentRunMode: a2aprovider.ReturnTaskWhen(func(context.Context, *a2asrv.ExecutorContext) (bool, error) { return false, nil }),
	})
	events := collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "ctx")))
	singleHostedMessage(t, events)
	if len(responseModeTasks(events)) != 0 {
		t.Fatal("unexpected task event")
	}
}

func TestResponseMode_DynamicTask(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Test response"), a2aprovider.ExecutorConfig{
		AgentRunMode: a2aprovider.ReturnTaskWhen(func(context.Context, *a2asrv.ExecutorContext) (bool, error) { return true, nil }),
	})
	events := collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "ctx")))
	if len(responseModeTasks(events)) != 1 {
		t.Fatal("expected one task event")
	}
	for _, event := range events {
		if _, ok := event.(*a2a.Message); ok {
			t.Fatal("unexpected message event")
		}
	}
}

func TestResponseMode_DynamicReceivesExecutorContext(t *testing.T) {
	var captured *a2asrv.ExecutorContext
	e := hostedResponseExecutor(hostedReply("Test response"), a2aprovider.ExecutorConfig{
		AgentRunMode: a2aprovider.ReturnTaskWhen(func(_ context.Context, request *a2asrv.ExecutorContext) (bool, error) {
			captured = request
			return false, nil
		}),
	})
	request := hostedExecutorContext("my-task", "my-ctx")
	collectStreamingEvents(t, e.Execute(t.Context(), request))
	if captured != request {
		t.Fatal("callback did not receive the request executor context")
	}
}

func TestResponseMode_DynamicErrorDoesNotInvokeAgent(t *testing.T) {
	invoked := false
	wantErr := errors.New("Callback failed")
	e := a2aprovider.NewExecutor(newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		invoked = true
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(&agent.ResponseUpdate{}, nil) }
	}), a2aprovider.ExecutorConfig{
		AgentRunMode: a2aprovider.ReturnTaskWhen(func(context.Context, *a2asrv.ExecutorContext) (bool, error) { return false, wantErr }),
	})
	_, err := collectStreamingEventsAndError(e.Execute(t.Context(), hostedExecutorContext("", "ctx")))
	if !errors.Is(err, wantErr) || invoked {
		t.Fatalf("error = %v, agent invoked = %t", err, invoked)
	}
}

func TestResponseMode_DynamicReceivesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var captured context.Context
	e := hostedResponseExecutor(hostedReply("Test response"), a2aprovider.ExecutorConfig{
		AgentRunMode: a2aprovider.ReturnTaskWhen(func(ctx context.Context, _ *a2asrv.ExecutorContext) (bool, error) {
			captured = ctx
			return false, nil
		}),
	})
	collectStreamingEvents(t, e.Execute(ctx, hostedExecutorContext("", "ctx")))
	if captured == nil || captured.Done() != ctx.Done() {
		t.Fatal("callback did not receive caller cancellation")
	}
}

func TestResponseMode_ContinuationSkipsDynamicCallback(t *testing.T) {
	invoked := false
	e := hostedResponseExecutor(hostedReply("Test response"), a2aprovider.ExecutorConfig{
		AgentRunMode: a2aprovider.ReturnTaskWhen(func(context.Context, *a2asrv.ExecutorContext) (bool, error) {
			invoked = true
			return false, nil
		}),
	})
	collectStreamingEvents(t, e.Execute(t.Context(), hostedContinuationContext()))
	if invoked {
		t.Fatal("task continuation invoked dynamic callback")
	}
}

func TestResponseMode_MessageStreamAggregatesAllUpdates(t *testing.T) {
	first := hostedUpdate("m1", "")
	first.ContinuationToken = agenttest.NewContinuationToken(t, "inner-token")
	last := hostedUpdate("m1", "")
	last.ContinuationToken = first.ContinuationToken
	e := responseModeExecutor([]*agent.ResponseUpdate{first, hostedUpdate("m1", "chunk 1"), hostedUpdate("m1", "chunk 2"), last}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	msg := singleHostedMessage(t, collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx"))))
	if len(msg.Parts) != 1 || msg.Parts[0].Text() != "chunk 1chunk 2" {
		t.Fatalf("message parts = %#v, want one aggregated text part", msg.Parts)
	}
}

func TestResponseMode_MessageResponseID(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{{Role: message.RoleAssistant, ResponseID: "resp-42", Contents: message.Contents{&message.TextContent{Text: "chunk"}}}}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	msg := singleHostedMessage(t, collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx"))))
	if msg.ID != "resp-42" {
		t.Fatalf("message ID = %q, want resp-42", msg.ID)
	}
}

func TestResponseMode_MessageGeneratesID(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "chunk"}}}}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	msg := singleHostedMessage(t, collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx"))))
	if msg.ID == "" {
		t.Fatal("message ID is empty")
	}
}

func TestResponseMode_MessageStreamMetadata(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{{Role: message.RoleAssistant, ResponseID: "r1", AdditionalProperties: map[string]any{"streamKey": "streamValue"}, Contents: message.Contents{&message.TextContent{Text: "chunk"}}}}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	msg := singleHostedMessage(t, collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx"))))
	if _, ok := msg.Metadata["streamKey"]; !ok {
		t.Fatal("message metadata is missing streamKey")
	}
}

func TestResponseMode_MessageStreamNullMetadata(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{{Role: message.RoleAssistant, ResponseID: "r1", Contents: message.Contents{&message.TextContent{Text: "chunk"}}}}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	msg := singleHostedMessage(t, collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx"))))
	if msg.Metadata != nil {
		t.Fatalf("metadata = %#v, want nil", msg.Metadata)
	}
}

func TestResponseMode_TaskStream(t *testing.T) {
	second := hostedUpdate("m1", "chunk 2")
	second.ContinuationToken = agenttest.NewContinuationToken(t, "inner-token")
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", "chunk 1"), second, hostedUpdate("m2", "chunk 3"), hostedUpdate("m2", "")}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	assertResponseModeSubmitted(t, events)
	statuses := assertHostedStatuses(t, events, a2a.TaskStateWorking, a2a.TaskStateCompleted)
	if statuses[0].Status.Message != nil {
		t.Fatal("working status unexpectedly contains a message")
	}
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 3 {
		t.Fatalf("artifact updates = %d, want 3", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "chunk 1", "m1", false, false)
	assertHostedArtifact(t, artifacts[1], "chunk 2", "m1", true, true)
	assertHostedArtifact(t, artifacts[2], "chunk 3", "m2", false, true)
}

func TestResponseMode_TaskStreamResponseIDDoesNotReserveMessageID(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{
		{ResponseID: "r1"}, hostedUpdate("r1", "reply"),
	}, nil, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	artifacts := collectStreamingArtifacts(collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx"))))
	if len(artifacts) != 1 {
		t.Fatalf("artifact updates = %d, want 1", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "reply", "r1", false, true)
}

func TestResponseMode_TaskStreamFailure(t *testing.T) {
	wantErr := errors.New("Stream failed")
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", "chunk 1")}, wantErr,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want original stream error", err)
	}
	assertResponseModeSubmitted(t, events)
	statuses := assertHostedStatuses(t, events, a2a.TaskStateWorking, a2a.TaskStateFailed)
	failure := statuses[1].Status.Message
	if failure == nil || len(failure.Parts) != 1 {
		t.Fatal("expected one generic failure text part")
	}
	text := failure.Parts[0].Text()
	if strings.Contains(text, wantErr.Error()) || text != "The agent encountered an unexpected error and could not complete the request." {
		t.Fatalf("failure message = %q", text)
	}
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 1 {
		t.Fatalf("artifact updates = %d, want 1", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "chunk 1", "", false, true)
}

func TestResponseMode_TaskStreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e := a2aprovider.NewExecutor(newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(hostedUpdate("m1", "chunk 1"), nil) {
				return
			}
			cancel()
			yield(nil, ctx.Err())
		}
	}), a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(directExecutorStream(ctx, e, hostedExecutorContext("task-1", "ctx")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	assertResponseModeSubmitted(t, events)
	assertHostedStatuses(t, events, a2a.TaskStateWorking, a2a.TaskStateCanceled)
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 1 {
		t.Fatalf("artifact updates = %d, want 1", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "chunk 1", "", false, true)
}

func TestResponseMode_TaskStreamAgentCancellationIsFailure(t *testing.T) {
	e := responseModeExecutor(nil, context.Canceled, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want original cancellation error", err)
	}
	statuses := collectStreamingStatuses(events)
	if len(statuses) == 0 || statuses[len(statuses)-1].Status.State != a2a.TaskStateFailed {
		t.Fatal("uncanceled caller should receive a failed task")
	}
}

func TestResponseMode_TaskStreamBoundaryBeforeFailure(t *testing.T) {
	wantErr := errors.New("Stream failed")
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", "m1 chunk"), hostedUpdate("m2", "m2 chunk")}, wantErr,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want original stream error", err)
	}
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 2 {
		t.Fatalf("artifact updates = %d, want 2", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "m1 chunk", "m1", false, true)
	assertHostedArtifact(t, artifacts[1], "m2 chunk", "m2", false, true)
}

// Native aggregation emits a completed Task instead of an early submitted
// Task followed by queue updates. These are native-contract checks, not paired
// ports of the upstream event-queue assertions.
func TestResponseMode_CompletedTask(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", "chunk 1"), hostedUpdate("m1", "chunk 2")}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("task-1", "ctx")))
	if len(events) != 1 {
		t.Fatalf("events = %d, want one completed task", len(events))
	}
	task, ok := events[0].(*a2a.Task)
	if !ok || task.Status.State != a2a.TaskStateCompleted || task.ID != "task-1" || task.ContextID != "ctx" {
		t.Fatalf("result = %#v, want completed task-1 in ctx", events[0])
	}
	if len(task.Artifacts) != 1 || len(task.Artifacts[0].Parts) != 1 || task.Artifacts[0].Parts[0].Text() != "chunk 1chunk 2" {
		t.Fatalf("artifacts = %#v, want aggregated text", task.Artifacts)
	}
}

func TestResponseMode_MessagePrefersLastResponseID(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{
		{ResponseID: "first-response", MessageID: "m1", Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "first"}}},
		{ResponseID: "last-response", MessageID: "m2", Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "last"}}},
	}, nil, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	request := hostedExecutorContext("task-1", "ctx")
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), request)))
	if msg.ID != "last-response" || msg.TaskID != "" || request.TaskID != "task-1" {
		t.Fatalf("message = %#v, request = %#v", msg, request)
	}
}

func TestResponseMode_TaskStreamEarlyStopClosesRun(t *testing.T) {
	closed := false
	count := 0
	e := a2aprovider.NewExecutor(newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			defer func() { closed = true }()
			for _, text := range []string{"first", "second", "third"} {
				count++
				if !yield(hostedUpdate("m1", text), nil) {
					return
				}
			}
		}
	}), a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	for event, err := range directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx")) {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := event.(*a2a.TaskArtifactUpdateEvent); ok {
			break
		}
	}
	if !closed || count != 2 {
		t.Fatalf("run closed = %t, consumed updates = %d, want true, 2", closed, count)
	}
}

// This Go value-copy regression is separate from the pinned .NET ports.
func TestAgentRunModeDynamicConfigCopy(t *testing.T) {
	var captured []*a2asrv.ExecutorContext
	cfg := a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTaskWhen(func(_ context.Context, request *a2asrv.ExecutorContext) (bool, error) {
		captured = append(captured, request)
		return request.ContextID == "task-context", nil
	})}
	copied := cfg
	cfg.AgentRunMode = a2aprovider.ReturnMessage()
	e := hostedResponseExecutor(hostedReply("reply"), copied)
	requests := []*a2asrv.ExecutorContext{
		hostedExecutorContext("", "task-context"),
		hostedExecutorContext("", "message-context"),
	}
	for i, request := range requests {
		events := collectStreamingEvents(t, e.Execute(t.Context(), request))
		if i == 0 {
			if tasks := responseModeTasks(events); len(events) != 1 || len(tasks) != 1 || tasks[0].Status.State != a2a.TaskStateCompleted {
				t.Fatalf("events = %#v, want one completed task", events)
			}
		} else {
			singleHostedMessage(t, events)
			if len(events) != 1 {
				t.Fatalf("events = %#v, want one message", events)
			}
		}
		if len(captured) != i+1 || captured[i] != request {
			t.Fatalf("callback requests = %#v, want each new request exactly once", captured)
		}
	}
}

func TestResponseMode_AggregationFailureEmitsNoPartialResult(t *testing.T) {
	wantErr := errors.New("run failed")
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", "partial")}, wantErr,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	events, err := collectStreamingEventsAndError(e.Execute(t.Context(), hostedExecutorContext("task-1", "ctx")))
	if !errors.Is(err, wantErr) || len(events) != 0 {
		t.Fatalf("error = %v, events = %#v, want original error and no events", err, events)
	}
}

func TestResponseMode_CompletedTaskNoUpdates(t *testing.T) {
	e := responseModeExecutor(nil, nil, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "ctx")))
	tasks := responseModeTasks(events)
	if len(tasks) != 1 || tasks[0].ID == "" || tasks[0].Status.State != a2a.TaskStateCompleted || len(tasks[0].Artifacts) != 0 {
		t.Fatalf("tasks = %#v, want completed task without artifacts", tasks)
	}
}

func TestResponseMode_NativeCompletedTask(t *testing.T) {
	h := a2aprovider.NewHandler(responseModeAgent([]*agent.ResponseUpdate{hostedUpdate("m1", "chunk 1"), hostedUpdate("m1", "chunk 2")}, nil),
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	result, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello")),
		Config:  &a2a.SendMessageConfig{ReturnImmediately: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := result.(*a2a.Task)
	if !ok || task.Status.State != a2a.TaskStateCompleted || len(task.Artifacts) != 1 || len(task.Artifacts[0].Parts) != 1 || task.Artifacts[0].Parts[0].Text() != "chunk 1chunk 2" {
		t.Fatalf("result = %#v, want completed task with aggregated text", result)
	}
}

func TestResponseMode_NativeCompletedTaskFailure(t *testing.T) {
	wantErr := errors.New("Stream failed")
	h := a2aprovider.NewHandler(responseModeAgent([]*agent.ResponseUpdate{hostedUpdate("m1", "partial")}, wantErr),
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	result, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello")),
		Config:  &a2a.SendMessageConfig{ReturnImmediately: false},
	})
	if !errors.Is(err, wantErr) || result != nil {
		t.Fatalf("result = %#v, error = %v, want original stream error and no task", result, err)
	}
}

func TestResponseMode_NativeTaskStream(t *testing.T) {
	h := a2aprovider.NewHandler(responseModeAgent([]*agent.ResponseUpdate{hostedUpdate("m1", "chunk 1"), hostedUpdate("m1", "chunk 2")}, nil),
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, h.SendStreamingMessage(t.Context(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello")),
	}))
	assertHostedStatuses(t, events, a2a.TaskStateWorking, a2a.TaskStateCompleted)
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 2 {
		t.Fatalf("artifact updates = %d, want 2", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "chunk 1", "m1", false, false)
	assertHostedArtifact(t, artifacts[1], "chunk 2", "m1", true, true)
}

func TestResponseMode_NativeTaskStreamFailurePreservesArtifacts(t *testing.T) {
	h := a2aprovider.NewHandler(responseModeAgent([]*agent.ResponseUpdate{hostedUpdate("m1", "chunk 1")}, errors.New("Stream failed")),
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(h.SendStreamingMessage(t.Context(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello")),
	}))
	if err != nil {
		t.Fatalf("native stream error = %v, want terminal failed status without queue cancellation", err)
	}
	statuses := assertHostedStatuses(t, events, a2a.TaskStateWorking, a2a.TaskStateFailed)
	if failure := statuses[1].Status.Message; failure == nil || len(failure.Parts) != 1 || failure.Parts[0].Text() != "The agent encountered an unexpected error and could not complete the request." {
		t.Fatalf("failure status message = %#v", failure)
	}
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 1 {
		t.Fatalf("artifact updates = %d, want buffered artifact preserved", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "chunk 1", "m1", false, true)
}

func TestResponseMode_CompletedTaskMessageBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		updates []*agent.ResponseUpdate
		texts   []string
	}{
		{"distinct", []*agent.ResponseUpdate{hostedUpdate("m1", "first"), hostedUpdate("m2", "second")}, []string{"first", "second"}},
		{"missing", []*agent.ResponseUpdate{hostedUpdate("m1", "first"), hostedUpdate("", " chunk"), hostedUpdate("m2", "second"), hostedUpdate("", " chunk")}, []string{"first chunk", "second chunk"}},
		{"empty", []*agent.ResponseUpdate{hostedUpdate("", "first"), hostedUpdate("", " second")}, []string{"first second"}},
		{"initial empty", []*agent.ResponseUpdate{hostedUpdate("", "first"), hostedUpdate("m1", "second")}, []string{"firstsecond"}},
		{"reappearing", []*agent.ResponseUpdate{hostedUpdate("m1", "first"), hostedUpdate("m2", "second"), hostedUpdate("m1", "third"), hostedUpdate("m1", " chunk")}, []string{"first", "second", "third chunk"}},
		{"contentless boundary", []*agent.ResponseUpdate{hostedUpdate("m1", "first"), hostedUpdate("m2", ""), hostedUpdate("m2", "second")}, []string{"first", "second"}},
		{"contentless", []*agent.ResponseUpdate{hostedUpdate("m1", ""), hostedUpdate("m2", "")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := responseModeExecutor(tc.updates, nil, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
			request := hostedExecutorContext("task-1", "ctx")
			events := collectStreamingEvents(t, e.Execute(t.Context(), request))
			tasks := responseModeTasks(events)
			if len(events) != 1 || len(tasks) != 1 || tasks[0].Status.State != a2a.TaskStateCompleted {
				t.Fatalf("events = %#v, want one completed task", events)
			}
			task := tasks[0]
			wantArtifacts := 0
			if len(tc.texts) > 0 {
				wantArtifacts = 1
			}
			if len(task.Artifacts) != wantArtifacts || task.ID != "task-1" || task.ContextID != "ctx" {
				t.Fatalf("task = %#v, want %d artifacts in task-1/ctx", task, wantArtifacts)
			}
			if wantArtifacts != 0 {
				artifact := task.Artifacts[0]
				if artifact.ID == "" || artifact.ID == "m1" || artifact.ID == "m2" || artifact.ID == "r1" {
					t.Fatalf("artifact ID = %q, want generated ID", artifact.ID)
				}
				if len(artifact.Parts) != len(tc.texts) {
					t.Fatalf("parts = %#v, want %d ordered parts", artifact.Parts, len(tc.texts))
				}
				for i, text := range tc.texts {
					if artifact.Parts[i].Text() != text {
						t.Fatalf("part %d text = %q, want %q", i, artifact.Parts[i].Text(), text)
					}
				}
			}
			if request.TaskID != "task-1" || request.ContextID != "ctx" {
				t.Fatal("executor modified the request IDs")
			}
		})
	}
}

func TestResponseMode_CompletedTaskFailurePreservesArtifacts(t *testing.T) {
	wantErr := errors.New("Stream failed")
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", "first"), hostedUpdate("m2", "second")}, wantErr,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(e.Execute(t.Context(), hostedExecutorContext("task-1", "ctx")))
	if !errors.Is(err, wantErr) || len(events) != 0 {
		t.Fatalf("events = %#v, error = %v, want original stream error and no events", events, err)
	}
}

func TestResponseMode_CompletedTaskCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	a := newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(hostedUpdate("m1", "partial"), nil) {
				return
			}
			cancel()
			yield(nil, ctx.Err())
		}
	})
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(e.Execute(ctx, hostedExecutorContext("task-1", "ctx")))
	if !errors.Is(err, context.Canceled) || len(events) != 0 {
		t.Fatalf("events = %#v, error = %v, want original cancellation error and no events", events, err)
	}
}

func TestResponseMode_CompletedTaskAgentCancellationIsFailure(t *testing.T) {
	e := responseModeExecutor(nil, context.Canceled, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(e.Execute(t.Context(), hostedExecutorContext("task-1", "ctx")))
	if !errors.Is(err, context.Canceled) || len(events) != 0 {
		t.Fatalf("events = %#v, error = %v, want original cancellation error and no events", events, err)
	}
}

func TestResponseMode_CompletedTaskMetadata(t *testing.T) {
	first := hostedUpdate("m1", "first")
	first.AdditionalProperties = map[string]any{"message": "first", "shared": "message value"}
	metadataOnly := hostedUpdate("m1", "")
	metadataOnly.AdditionalProperties = map[string]any{"late": "first metadata"}
	second := hostedUpdate("m2", "second")
	second.AdditionalProperties = map[string]any{"message": "second"}
	responseMetadata := &agent.ResponseUpdate{AdditionalProperties: map[string]any{"response": "response metadata", "shared": "response value"}}
	e := responseModeExecutor([]*agent.ResponseUpdate{first, metadataOnly, second, responseMetadata}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	tasks := responseModeTasks(collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("task-1", "ctx"))))
	if len(tasks) != 1 || len(tasks[0].Artifacts) != 1 {
		t.Fatalf("tasks = %#v, want one artifact", tasks)
	}
	want := map[string]any{"response": "response metadata", "shared": "response value"}
	if !reflect.DeepEqual(tasks[0].Artifacts[0].Metadata, want) {
		t.Fatalf("artifact metadata = %#v, want response-only metadata %#v", tasks[0].Artifacts[0].Metadata, want)
	}
	if !reflect.DeepEqual(first.AdditionalProperties, map[string]any{"message": "first", "shared": "message value"}) || len(responseMetadata.AdditionalProperties) != 2 {
		t.Fatal("aggregation modified agent metadata")
	}
}

func TestResponseMode_CompletedTaskConversionFailure(t *testing.T) {
	content := &message.DataContent{Data: "invalid base64!", MediaType: "application/octet-stream"}
	_, wantErr := content.Bytes()
	if wantErr == nil {
		t.Fatal("expected invalid data fixture")
	}
	invalid := &agent.ResponseUpdate{MessageID: "m2", Contents: message.Contents{content}}
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", "partial"), invalid}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events, err := collectStreamingEventsAndError(e.Execute(t.Context(), hostedExecutorContext("task-1", "ctx")))
	tasks := responseModeTasks(events)
	if !errors.Is(err, wantErr) || len(events) != 1 || len(tasks) != 1 || tasks[0].Status.State != a2a.TaskStateFailed || len(tasks[0].Artifacts) != 0 {
		t.Fatalf("events = %#v, error = %v, want failed task without artifacts and original conversion error", events, err)
	}
	task := tasks[0]
	if task.ID != "task-1" || task.ContextID != "ctx" {
		t.Fatalf("task = %#v, want task-1 in ctx", task)
	}
	if failure := task.Status.Message; failure == nil || failure.TaskID != task.ID || failure.ContextID != task.ContextID || len(failure.Parts) != 1 || failure.Parts[0].Text() != "The agent encountered an unexpected error and could not complete the request." {
		t.Fatalf("failure message = %#v, want generic failure status", failure)
	}
}

func TestResponseMode_NativeCompletedTaskConversionFailure(t *testing.T) {
	invalid := &agent.ResponseUpdate{MessageID: "m2", Contents: message.Contents{&message.DataContent{Data: "invalid base64!", MediaType: "application/octet-stream"}}}
	h := a2aprovider.NewHandler(responseModeAgent([]*agent.ResponseUpdate{hostedUpdate("m1", "partial"), invalid}, nil),
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	result, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello")),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := result.(*a2a.Task)
	if !ok || task.Status.State != a2a.TaskStateFailed || len(task.Artifacts) != 0 {
		t.Fatalf("result = %#v, want failed task without artifacts", result)
	}
	failure := task.Status.Message
	if failure == nil || len(failure.Parts) != 1 || failure.Parts[0].Text() != "The agent encountered an unexpected error and could not complete the request." {
		t.Fatalf("failure message = %#v, want generic failure", failure)
	}
}

// Configuration is captured at the native handler boundary, not by direct
// executors. These checks are not direct ports of the .NET queue entrypoint.
func TestResponseMode_ConfigurationAndMetadataForwarded(t *testing.T) {
	requestConfig := &a2a.SendMessageConfig{HistoryLength: new(5)}
	var captured []agent.Option
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = options
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("m1", "reply"), nil) }
	})
	h := a2aprovider.NewHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	_, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{
		Message: hostedExecutorContext("", "ctx").Message,
		Config:  requestConfig,
		Metadata: map[string]any{
			"key1": "value1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration, ok := agent.GetOption(captured, a2aprovider.WithConfiguration)
	metadata, _ := agent.GetOption(captured, a2aprovider.WithMetadata)
	if !ok || !reflect.DeepEqual(configuration, requestConfig) || len(metadata) != 1 || metadata["key1"] != "value1" {
		t.Fatalf("configuration = %#v, metadata = %#v", configuration, metadata)
	}
}

func TestResponseMode_ConfigurationImmediateDoesNotEnableBackground(t *testing.T) {
	var captured []agent.Option
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = options
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("m1", "reply"), nil) }
	})
	h := a2aprovider.NewHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	requestConfig := &a2a.SendMessageConfig{ReturnImmediately: true}
	_, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: hostedExecutorContext("", "ctx").Message, Config: requestConfig})
	if err != nil {
		t.Fatal(err)
	}
	background, hasBackground := agent.GetOption(captured, agent.AllowBackgroundResponses)
	configuration, _ := agent.GetOption(captured, a2aprovider.WithConfiguration)
	if hasBackground || configuration == nil || !configuration.ReturnImmediately {
		t.Fatalf("background = (%t, %t), configuration = %#v, want unset background option", background, hasBackground, configuration)
	}
}

func TestResponseMode_StreamingConfigurationForwarded(t *testing.T) {
	var captured []agent.Option
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = options
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("m1", "reply"), nil) }
	})
	h := a2aprovider.NewHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	requestConfig := &a2a.SendMessageConfig{AcceptedOutputModes: []string{"text/plain"}}
	collectStreamingEvents(t, h.SendStreamingMessage(t.Context(), &a2a.SendMessageRequest{Message: hostedExecutorContext("", "ctx").Message, Config: requestConfig}))
	configuration, ok := agent.GetOption(captured, a2aprovider.WithConfiguration)
	background, hasBackground := agent.GetOption(captured, agent.AllowBackgroundResponses)
	if !ok || hasBackground || !reflect.DeepEqual(configuration, requestConfig) {
		t.Fatalf("configuration = %#v, background = (%t, %t), want unset background option", configuration, background, hasBackground)
	}
}

func TestResponseMode_PreservesAgentBackgroundDefault(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mode         a2aprovider.AgentRunMode
		streaming    bool
		continuation bool
	}{
		{name: "message"},
		{name: "message stream", streaming: true},
		{name: "task", mode: a2aprovider.ReturnTask()},
		{name: "task stream", mode: a2aprovider.ReturnTask(), streaming: true},
		{name: "continuation", continuation: true},
		{name: "continuation stream", continuation: true, streaming: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := agent.New(agent.ProviderConfig{Run: func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
				return func(yield func(*agent.ResponseUpdate, error) bool) {
					background, _ := agent.GetOption(options, agent.AllowBackgroundResponses)
					if !background {
						yield(nil, errors.New("hosting overrode the agent's background default"))
						return
					}
					yield(hostedUpdate("m1", "reply"), nil)
				}
			}}, agent.Config{RunOptions: []agent.Option{agent.AllowBackgroundResponses(true)}})
			e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{AgentRunMode: tc.mode})
			request := hostedExecutorContext("", "ctx")
			if tc.continuation {
				request = hostedContinuationContext()
			}
			seq := e.Execute(t.Context(), request)
			if tc.streaming {
				seq = directExecutorStream(t.Context(), e, request)
			}
			collectStreamingEvents(t, seq)
		})
	}
}

func TestResponseMode_FailureSavesSessionWithoutCancellation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "completed"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			store := &hostedSessionStore{session: new(agent.Session), saveErr: errors.New("save failed")}
			wantErr := errors.New("run failed")
			e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", "partial")}, wantErr,
				a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask(), SessionStore: store})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			seq := e.Execute(ctx, hostedExecutorContext("task-1", "ctx"))
			if streaming {
				seq = directExecutorStream(ctx, e, hostedExecutorContext("task-1", "ctx"))
			}
			_, err := collectStreamingEventsAndError(seq)
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want original run error", err)
			}
			assertHostedSessionSaved(t, store, "ctx", true)
		})
	}
}

type responseModePushSender struct{ events chan<- a2a.Event }

func (s responseModePushSender) SendPush(_ context.Context, _ *a2a.PushConfig, event a2a.Event) error {
	if s.events != nil {
		s.events <- event
	}
	return nil
}

func TestResponseMode_FullConfigurationSnapshot(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, immediate := range []bool{false, true} {
			name := "send/wait"
			if streaming {
				name = "stream/wait"
			}
			if immediate {
				name = strings.ReplaceAll(name, "wait", "immediate")
			}
			t.Run(name, func(t *testing.T) {
				requestConfig := &a2a.SendMessageConfig{
					AcceptedOutputModes: []string{"text/plain", "image/png"},
					ReturnImmediately:   immediate,
					HistoryLength:       new(0),
					PushConfig: &a2a.PushConfig{
						ID: "callback", Tenant: "tenant", TaskID: "task", URL: "https://example.test/notifications", Token: "test token",
						Auth: &a2a.PushAuthInfo{Scheme: "Bearer", Credentials: "test credentials"},
					},
				}
				var captured *a2a.SendMessageConfig
				a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					captured, _ = agent.GetOption(options, a2aprovider.WithConfiguration)
					background, _ := agent.GetOption(options, agent.AllowBackgroundResponses)
					if background {
						return func(yield func(*agent.ResponseUpdate, error) bool) {
							yield(nil, errors.New("configuration enabled background responses"))
						}
					}
					return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("m1", "reply"), nil) }
				})
				h := a2aprovider.NewHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()},
					a2asrv.WithPushNotifications(push.NewInMemoryStore(), responseModePushSender{}))
				request := &a2a.SendMessageRequest{Message: hostedExecutorContext("", "ctx").Message, Config: requestConfig}
				if streaming {
					collectStreamingEvents(t, h.SendStreamingMessage(t.Context(), request))
				} else if _, err := h.SendMessage(t.Context(), request); err != nil {
					t.Fatal(err)
				}
				if captured == nil || captured == requestConfig || !reflect.DeepEqual(captured, requestConfig) {
					t.Fatalf("configuration = %#v, want complete snapshot of %#v", captured, requestConfig)
				}
				requestConfig.AcceptedOutputModes[0] = "changed"
				*requestConfig.HistoryLength = 42
				requestConfig.PushConfig.URL = "changed"
				requestConfig.PushConfig.Auth.Scheme = "changed"
				requestConfig.PushConfig.Auth.Credentials = "changed"
				if captured.AcceptedOutputModes[0] != "text/plain" || *captured.HistoryLength != 0 || captured.PushConfig.URL != "https://example.test/notifications" || captured.PushConfig.Auth.Scheme != "Bearer" || captured.PushConfig.Auth.Credentials != "test credentials" {
					t.Fatalf("snapshot changed with request: %#v", captured)
				}
			})
		}
	}
}

func TestResponseMode_ConfigurationDoesNotLeakAcrossRequests(t *testing.T) {
	var configurations []*a2a.SendMessageConfig
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		configuration, _ := agent.GetOption(options, a2aprovider.WithConfiguration)
		configurations = append(configurations, configuration)
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("m1", "reply"), nil) }
	})
	h := a2aprovider.NewHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	for _, requestConfig := range []*a2a.SendMessageConfig{{HistoryLength: new(5)}, nil, {HistoryLength: new(0)}} {
		if _, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello")), Config: requestConfig}); err != nil {
			t.Fatal(err)
		}
	}
	if len(configurations) != 3 || configurations[0] == nil || *configurations[0].HistoryLength != 5 || configurations[1] != nil || configurations[2] == nil || *configurations[2].HistoryLength != 0 {
		t.Fatalf("configurations = %#v, want independent 5, absent, and explicit zero", configurations)
	}
}

func TestResponseMode_NativeTaskReturnImmediately(t *testing.T) {
	release := make(chan struct{})
	defer func() {
		if release != nil {
			close(release)
		}
	}()
	updates := []*agent.ResponseUpdate{hostedUpdate("m1", "chunk 1"), hostedUpdate("m1", "chunk 2")}
	updates[1].ContinuationToken = agenttest.NewContinuationToken(t, "inner-token")
	gate := release
	a := newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(updates[0], nil) {
				return
			}
			<-gate
			yield(updates[1], nil)
		}
	})
	pushEvents := make(chan a2a.Event, 5)
	h := a2aprovider.NewHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()},
		a2asrv.WithPushNotifications(push.NewInMemoryStore(), responseModePushSender{events: pushEvents}))
	requestConfig := &a2a.SendMessageConfig{ReturnImmediately: true, PushConfig: &a2a.PushConfig{URL: "https://example.test/notifications"}}
	result, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("Hello")), Config: requestConfig})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := result.(*a2a.Task)
	if !ok || task.Status.State != a2a.TaskStateSubmitted && task.Status.State != a2a.TaskStateWorking {
		t.Fatalf("result = %#v, want immediate in-progress task", result)
	}
	close(release)
	release = nil
	var events []a2a.Event
	for {
		event := <-pushEvents
		events = append(events, event)
		if status, ok := event.(*a2a.TaskStatusUpdateEvent); ok && status.Status.State == a2a.TaskStateCompleted {
			break
		}
	}
	assertResponseModeSubmitted(t, events)
	assertHostedStatuses(t, events, a2a.TaskStateWorking, a2a.TaskStateCompleted)
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 2 {
		t.Fatalf("artifact updates = %d, want 2", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "chunk 1", "m1", false, false)
	assertHostedArtifact(t, artifacts[1], "chunk 2", "m1", true, true)
	stored, err := h.GetTask(t.Context(), &a2a.GetTaskRequest{ID: task.ID})
	if err != nil || stored.Status.State != a2a.TaskStateCompleted || len(stored.Artifacts) != 1 {
		t.Fatalf("stored task = %#v, error = %v, want completed task", stored, err)
	}
}

func TestResponseMode_CompletedTaskMixedParts(t *testing.T) {
	updates := []*agent.ResponseUpdate{
		{MessageID: "m1", Contents: message.Contents{&message.TextContent{Text: "first"}, &message.DataContent{Data: "YQ==", MediaType: "application/octet-stream"}}},
		{MessageID: "m1", Contents: message.Contents{&message.DataContent{Data: "Yg==", MediaType: "application/octet-stream"}, &message.TextContent{Text: "last"}}},
		{MessageID: "m2", Contents: message.Contents{&message.URIContent{URI: "https://example.test/image", MediaType: "image/png"}}},
	}
	e := responseModeExecutor(updates, nil, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	tasks := responseModeTasks(collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("task-1", "ctx"))))
	if len(tasks) != 1 || len(tasks[0].Artifacts) != 1 {
		t.Fatalf("tasks = %#v, want one artifact", tasks)
	}
	parts := tasks[0].Artifacts[0].Parts
	if len(parts) != 5 || parts[0].Text() != "first" || string(parts[1].Raw()) != "a" || string(parts[2].Raw()) != "b" || parts[1].MediaType != "application/octet-stream" || parts[2].MediaType != "application/octet-stream" || parts[3].Text() != "last" || parts[4].URL() != "https://example.test/image" || parts[4].MediaType != "image/png" {
		t.Fatalf("parts = %#v, want ordered text, separate binary parts, text, image URI", parts)
	}
}

func TestResponseMode_MessageStreamProvidedContextID(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("", "chunk")}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	msg := singleHostedMessage(t, collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", "my-context-id"))))
	if msg.ContextID != "my-context-id" {
		t.Fatalf("context ID = %q, want my-context-id", msg.ContextID)
	}
}

func TestResponseMode_MessageStreamGeneratesContextID(t *testing.T) {
	e := responseModeExecutor([]*agent.ResponseUpdate{hostedUpdate("", "chunk")}, nil,
		a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnMessage()})
	msg := singleHostedMessage(t, collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", ""))))
	if msg.ContextID == "" {
		t.Fatal("generated context ID is empty")
	}
}
