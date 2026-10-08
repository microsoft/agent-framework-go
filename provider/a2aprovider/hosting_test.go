// Copyright (c) Microsoft. All rights reserved.

package a2aprovider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

func newHostedTestAgent(runFn func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error]) *agent.Agent {
	return agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{Name: "test-agent", ID: "test-agent-id"})
}

func newRequestHandler(hostedAgent *agent.Agent, cfg a2aprovider.ExecutorConfig, options ...a2asrv.RequestHandlerOption) a2asrv.RequestHandler {
	return a2aprovider.NewHandler(hostedAgent, cfg, options...)
}

func TestNewExecutor_PanicsWithoutAgent(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when agent is nil")
		}
	}()
	_ = a2aprovider.NewExecutor(nil, a2aprovider.ExecutorConfig{})
}

func TestRequestHandler_OnSendMessage_WithReferenceTaskIDs_ReturnsError(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: "ignored"}}}, nil)
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{})
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))
	msg.ReferenceTasks = []a2a.TaskID{"task-123"}
	_, err := h.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err == nil {
		t.Fatal("expected error for referenceTaskIds")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "referencetaskids") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequestHandler_ContextIDIsNotProviderSessionIDAndMetadataIsForwarded(t *testing.T) {
	var serviceID string
	var runMetadata map[string]any
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		session, _ := agent.GetOption(options, agent.WithSession)
		serviceID = session.ServiceID()
		runMetadata, _ = agent.GetOption(options, a2aprovider.WithMetadata)
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil)
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{})
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))
	msg.ContextID = "a2a-context"
	_, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{
		Message:  msg,
		Metadata: map[string]any{"tenant": "contoso"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if serviceID != "" {
		t.Fatalf("Session.ServiceID() = %q, want empty", serviceID)
	}
	if runMetadata["tenant"] != "contoso" {
		t.Fatalf("run metadata = %#v, want tenant=contoso", runMetadata)
	}
}

func TestRequestHandler_OnSendMessageContinuation_UsesStoredTaskHistoryOnly(t *testing.T) {
	var callCount int
	var continuationInputs []string

	a := newHostedTestAgent(func(_ context.Context, messagesIn []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		callCount++
		continuationInputs = agentMessageTexts(messagesIn)

		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if callCount != 1 {
				yield(nil, assertErr("unexpected agent invocation"))
				return
			}
			yield(&agent.ResponseUpdate{
				MessageID: "m-done",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "done"}},
			}, nil)
		}
	})

	store := taskstore.NewInMemory(nil)
	seededTask := &a2a.Task{
		ID:        a2a.NewTaskID(),
		ContextID: "ctx-1",
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("original request")),
			a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("working")),
		},
	}
	if _, err := store.Create(context.Background(), seededTask); err != nil {
		t.Fatalf("task store Create returned error: %v", err)
	}

	h := newRequestHandler(
		a,
		a2aprovider.ExecutorConfig{},
		a2asrv.WithTaskStore(store),
	)
	storedTask, err := h.GetTask(context.Background(), &a2a.GetTaskRequest{ID: seededTask.ID})
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	continueMsg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue"))
	continueMsg.TaskID = seededTask.ID

	continued, err := h.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: continueMsg})
	if err != nil {
		t.Fatalf("continuation SendMessage returned error: %v", err)
	}
	continuedTask, ok := continued.(*a2a.Task)
	if !ok {
		t.Fatalf("continuation result type = %T, want *a2a.Task", continued)
	}
	if continuedTask.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("task status = %q, want %q", continuedTask.Status.State, a2a.TaskStateCompleted)
	}
	if callCount != 1 {
		t.Fatalf("agent call count = %d, want %d", callCount, 1)
	}
	expectedInputs := a2aMessageTexts(storedTask.History)
	if len(continuationInputs) != len(expectedInputs) {
		t.Fatalf("continuation input count = %d, want %d", len(continuationInputs), len(expectedInputs))
	}
	for i, got := range continuationInputs {
		if got != expectedInputs[i] {
			t.Fatalf("continuation input %d = %q, want %q", i, got, expectedInputs[i])
		}
	}
	for _, got := range continuationInputs {
		if got == "continue" {
			t.Fatal("expected continuation request message to be excluded from continuation inputs")
		}
	}
}

func TestRequestHandler_BackgroundResponse_PollsContinuationToken(t *testing.T) {
	var calls int
	var stream bool
	var runToken string
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		calls++
		stream, _ = agent.GetOption(options, agent.Stream)
		runToken, _ = agent.GetOption(options, agent.WithContinuationToken)
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if calls != 1 {
				yield(nil, assertErr("unexpected agent invocation"))
				return
			}
			if !yield(&agent.ResponseUpdate{
				MessageID:         "m1",
				Role:              message.RoleAssistant,
				Contents:          message.Contents{&message.TextContent{Text: "chunk 1"}},
				ContinuationToken: "inner-token",
			}, nil) {
				return
			}
			yield(hostedUpdate("m1", "chunk 2"), nil)
		}
	})
	store := taskstore.NewInMemory(nil)
	h := newRequestHandler(
		a,
		a2aprovider.ExecutorConfig{},
		a2asrv.WithTaskStore(store),
	)

	events := collectStreamingEvents(t, h.SendStreamingMessage(t.Context(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("start")),
	}))
	if calls != 1 || !stream || runToken != "" {
		t.Fatalf("agent run = (%d calls, stream=%t, token=%q), want one streaming call without token polling", calls, stream, runToken)
	}
	if len(events) != 1 || len(collectStreamingTasks(events)) != 0 || len(collectStreamingStatuses(events)) != 0 {
		t.Fatalf("events = %#v, want one message without task or status events", events)
	}
	msg := singleHostedMessage(t, events)
	if len(msg.Parts) != 1 || msg.Parts[0].Text() != "chunk 1chunk 2" {
		t.Fatalf("message parts = %#v, want one aggregated text part", msg.Parts)
	}
}

func TestRequestHandler_OnSendMessageContinuation_UsesStoredContinuationToken(t *testing.T) {
	var resumedToken string
	var resumedMessageCount int
	a := newHostedTestAgent(func(_ context.Context, messagesIn []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		resumedToken, _ = agent.GetOption(options, agent.WithContinuationToken)
		resumedMessageCount = len(messagesIn)
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "done"}}}, nil)
		}
	})
	storedTask := &a2a.Task{
		ID:        a2a.NewTaskID(),
		ContextID: "ctx-continuation",
		Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
		Metadata: map[string]any{
			"__a2a__continuationToken": agenttest.NewContinuationToken(t, "inner-token"),
		},
	}
	store := taskstore.NewInMemory(nil)
	if _, err := store.Create(t.Context(), storedTask); err != nil {
		t.Fatal(err)
	}
	h := newRequestHandler(a, a2aprovider.ExecutorConfig{}, a2asrv.WithTaskStore(store))
	continueMessage := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue"))
	continueMessage.TaskID = storedTask.ID
	continueMessage.ContextID = storedTask.ContextID

	continued, err := h.SendMessage(t.Context(), &a2a.SendMessageRequest{Message: continueMessage})
	if err != nil {
		t.Fatal(err)
	}
	continuedTask, ok := continued.(*a2a.Task)
	if !ok || continuedTask.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("continued result = %#v, want completed task", continued)
	}
	if resumedToken != "inner-token" {
		t.Fatalf("resumed token = %q, want %q", resumedToken, "inner-token")
	}
	if resumedMessageCount != 0 {
		t.Fatalf("resumed message count = %d, want 0", resumedMessageCount)
	}
}

func TestRequestHandler_OnSendStreamingMessageContinuation_UsesTaskUpdatePath(t *testing.T) {
	var callCount int
	var continuationInputs []string
	var continuationStream bool

	a := newHostedTestAgent(func(_ context.Context, messagesIn []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		callCount++
		continuationInputs = agentMessageTexts(messagesIn)
		continuationStream, _ = agent.GetOption(options, agent.Stream)

		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if callCount != 1 {
				yield(nil, assertErr("unexpected agent invocation"))
				return
			}
			yield(&agent.ResponseUpdate{
				MessageID: "m-done",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "done"}},
			}, nil)
		}
	})

	store := taskstore.NewInMemory(nil)
	seededTask := &a2a.Task{
		ID:        a2a.NewTaskID(),
		ContextID: "ctx-1",
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("original request")),
			a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("working")),
		},
	}
	if _, err := store.Create(context.Background(), seededTask); err != nil {
		t.Fatalf("task store Create returned error: %v", err)
	}

	h := newRequestHandler(
		a,
		a2aprovider.ExecutorConfig{},
		a2asrv.WithTaskStore(store),
	)
	storedTask, err := h.GetTask(context.Background(), &a2a.GetTaskRequest{ID: seededTask.ID})
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	continueMsg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("continue"))
	continueMsg.TaskID = seededTask.ID

	events := collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{Message: continueMsg}))
	if callCount != 1 {
		t.Fatalf("agent call count = %d, want %d", callCount, 1)
	}
	if continuationStream {
		t.Fatal("expected continuation request to use task update path without agent.Stream(true)")
	}
	expectedInputs := a2aMessageTexts(storedTask.History)
	if len(continuationInputs) != len(expectedInputs) {
		t.Fatalf("continuation input count = %d, want %d", len(continuationInputs), len(expectedInputs))
	}
	for i, got := range continuationInputs {
		if got != expectedInputs[i] {
			t.Fatalf("continuation input %d = %q, want %q", i, got, expectedInputs[i])
		}
	}
	for _, got := range continuationInputs {
		if got == "continue" {
			t.Fatal("expected continuation request message to be excluded from continuation inputs")
		}
	}
	if len(collectStreamingTasks(events)) != 0 {
		t.Fatalf("task event count = %d, want 0", len(collectStreamingTasks(events)))
	}
	statuses := collectStreamingStatuses(events)
	if len(statuses) != 1 {
		t.Fatalf("status event count = %d, want 1", len(statuses))
	}
	if statuses[0].Status.State != a2a.TaskStateCompleted {
		t.Fatalf("status = %q, want %q", statuses[0].Status.State, a2a.TaskStateCompleted)
	}
	if len(collectStreamingArtifacts(events)) != 1 {
		t.Fatalf("artifact event count = %d, want 1", len(collectStreamingArtifacts(events)))
	}
}

func TestRequestHandler_OnSendMessageStream_UsesTaskLifecycleAndArtifacts(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		stream, _ := agent.GetOption(options, agent.Stream)
		if !stream {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(nil, assertErr("expected Stream=true"))
			}
		}
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "chunk 1"}},
			}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{
				ResponseID: "r2",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "chunk 2"}},
			}, nil)
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	}))

	if len(collectStreamingTasks(events)) != 1 {
		t.Fatalf("task event count = %d, want 1", len(collectStreamingTasks(events)))
	}
	statuses := collectStreamingStatuses(events)
	if len(statuses) != 2 {
		t.Fatalf("status event count = %d, want 2", len(statuses))
	}
	if statuses[0].Status.State != a2a.TaskStateWorking || statuses[1].Status.State != a2a.TaskStateCompleted {
		t.Fatalf("unexpected status sequence: %q, %q", statuses[0].Status.State, statuses[1].Status.State)
	}
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 2 {
		t.Fatalf("artifact event count = %d, want 2", len(artifacts))
	}
	if got := a2aArtifactText(artifacts[0]); got != "chunk 1" {
		t.Fatalf("first streamed artifact text = %q, want %q", got, "chunk 1")
	}
	if got := a2aArtifactText(artifacts[1]); got != "chunk 2" {
		t.Fatalf("second streamed artifact text = %q, want %q", got, "chunk 2")
	}
	if artifacts[0].Artifact.ID == "" {
		t.Fatal("expected first streamed artifact id")
	}
	if artifacts[1].Artifact.ID != artifacts[0].Artifact.ID {
		t.Fatalf("artifact ids = %q, %q, want identical ids", artifacts[0].Artifact.ID, artifacts[1].Artifact.ID)
	}
	if artifacts[0].Append {
		t.Fatal("expected first streamed artifact update to start a new artifact")
	}
	if artifacts[0].LastChunk {
		t.Fatal("expected first streamed artifact update to remain open")
	}
	if !artifacts[1].Append {
		t.Fatal("expected second streamed artifact update to append to the same artifact")
	}
	if !artifacts[1].LastChunk {
		t.Fatal("expected second streamed artifact update to close the artifact")
	}
}

func TestRequestHandler_OnSendMessageStream_ReusedMessageIDGetsNewArtifactID(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "first"}},
			}, nil) {
				return
			}
			if !yield(&agent.ResponseUpdate{
				MessageID: "msg-2",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "second"}},
			}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "third"}},
			}, nil)
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	artifacts := collectStreamingArtifacts(collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	})))

	if len(artifacts) != 3 {
		t.Fatalf("artifact event count = %d, want 3", len(artifacts))
	}
	if artifacts[0].Artifact.ID != "msg-1" {
		t.Fatalf("first artifact id = %q, want %q", artifacts[0].Artifact.ID, "msg-1")
	}
	if artifacts[1].Artifact.ID != "msg-2" {
		t.Fatalf("second artifact id = %q, want %q", artifacts[1].Artifact.ID, "msg-2")
	}
	if artifacts[2].Artifact.ID == "" || artifacts[2].Artifact.ID == "msg-1" {
		t.Fatalf("third artifact id = %q, want generated id distinct from %q", artifacts[2].Artifact.ID, "msg-1")
	}
	if !artifacts[0].LastChunk || !artifacts[1].LastChunk || !artifacts[2].LastChunk {
		t.Fatalf("expected each single-update artifact to be closed: %#v %#v %#v", artifacts[0].LastChunk, artifacts[1].LastChunk, artifacts[2].LastChunk)
	}
}

func TestRequestHandler_OnSendMessageStream_MissingMessageIDFallsBackToResponseIDForArtifactID(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				ResponseID: "resp-1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "reply"}},
			}, nil)
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	artifacts := collectStreamingArtifacts(collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	})))

	if len(artifacts) != 1 {
		t.Fatalf("artifact event count = %d, want 1", len(artifacts))
	}
	if artifacts[0].Artifact.ID == "" || artifacts[0].Artifact.ID == "resp-1" {
		t.Fatalf("artifact id = %q, want generated ID independent of response ID", artifacts[0].Artifact.ID)
	}
}

func TestRequestHandler_OnSendMessageStream_FlushesBufferedArtifactBeforeFailure(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "partial"}},
			}, nil) {
				return
			}
			yield(nil, assertErr("boom"))
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	}))

	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 1 {
		t.Fatalf("artifact event count = %d, want 1", len(artifacts))
	}
	if got := a2aArtifactText(artifacts[0]); got != "partial" {
		t.Fatalf("artifact text = %q, want %q", got, "partial")
	}
	if artifacts[0].Append {
		t.Fatal("expected failure artifact flush to start a new artifact")
	}
	if !artifacts[0].LastChunk {
		t.Fatal("expected failure artifact flush to close the artifact")
	}

	statuses := collectStreamingStatuses(events)
	if len(statuses) != 2 {
		t.Fatalf("status event count = %d, want 2", len(statuses))
	}
	if statuses[0].Status.State != a2a.TaskStateWorking || statuses[1].Status.State != a2a.TaskStateFailed {
		t.Fatalf("unexpected status sequence: %q, %q", statuses[0].Status.State, statuses[1].Status.State)
	}
	const want = "The agent encountered an unexpected error and could not complete the request."
	if statuses[1].Status.Message == nil || statuses[1].Status.Message.Parts[0].Text() != want {
		t.Fatalf("failed status message = %#v, want %q", statuses[1].Status.Message, want)
	}
}

func TestRequestHandler_OnSendMessageStream_UsesProvidedContextID(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "reply"}},
			}, nil)
		}
	})

	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))}
	req.Message.ContextID = "ctx-stream"

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), req))

	if len(events) == 0 {
		t.Fatal("expected streamed events")
	}
	for _, task := range collectStreamingTasks(events) {
		if task.ContextID != "ctx-stream" {
			t.Fatalf("task context id = %q, want %q", task.ContextID, "ctx-stream")
		}
	}
	for _, status := range collectStreamingStatuses(events) {
		if status.ContextID != "ctx-stream" {
			t.Fatalf("status context id = %q, want %q", status.ContextID, "ctx-stream")
		}
	}
	for _, artifact := range collectStreamingArtifacts(events) {
		if artifact.ContextID != "ctx-stream" {
			t.Fatalf("artifact context id = %q, want %q", artifact.ContextID, "ctx-stream")
		}
	}
}

func TestRequestHandler_OnSendMessageStream_GeneratesContextIDWhenMissing(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "reply"}},
			}, nil)
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	}))

	tasks := collectStreamingTasks(events)
	if len(tasks) != 1 {
		t.Fatalf("task event count = %d, want 1", len(tasks))
	}
	if tasks[0].ContextID == "" {
		t.Fatal("expected generated context id")
	}
}

func TestRequestHandler_OnSendMessageStream_WhenMessageIsNil_ReturnsError(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(func(*agent.ResponseUpdate, error) bool) {}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{})
	var gotErr error
	for evt, err := range h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{}) {
		if evt != nil {
			t.Fatalf("unexpected event: %#v", evt)
		}
		gotErr = err
	}
	if gotErr == nil {
		t.Fatal("expected error")
	}
	if gotErr.Error() != "message is required: invalid params" {
		t.Fatalf("error = %v, want %q", gotErr, "message is required: invalid params")
	}
}

func TestRequestHandler_OnSendMessageStream_WithResponseAdditionalProperties_SetsArtifactMetadata(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "reply"}},
				AdditionalProperties: map[string]any{
					"streamKey": "streamValue",
					"count":     2,
				},
			}, nil)
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	artifacts := collectStreamingArtifacts(collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	})))

	if len(artifacts) != 1 {
		t.Fatalf("artifact event count = %d, want 1", len(artifacts))
	}
	if got := artifacts[0].Metadata["streamKey"]; got != "streamValue" {
		t.Fatalf("metadata streamKey = %v, want %q", got, "streamValue")
	}
	if got := artifacts[0].Metadata["count"]; got != 2 {
		t.Fatalf("metadata count = %v, want %d", got, 2)
	}
}

func TestRequestHandler_OnSendMessageStream_WithNilAdditionalProperties_LeavesArtifactMetadataNil(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "reply"}},
			}, nil)
		}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	artifacts := collectStreamingArtifacts(collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	})))

	if len(artifacts) != 1 {
		t.Fatalf("artifact event count = %d, want 1", len(artifacts))
	}
	if artifacts[0].Metadata != nil {
		t.Fatalf("expected nil metadata, got %#v", artifacts[0].Metadata)
	}
}

func TestRequestHandler_OnSendMessageStream_WhenAgentYieldsNoUpdates_ReturnsLifecycleOnly(t *testing.T) {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(func(*agent.ResponseUpdate, error) bool) {}
	})

	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
	events := collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	}))

	if len(collectStreamingTasks(events)) != 1 {
		t.Fatalf("task event count = %d, want 1", len(collectStreamingTasks(events)))
	}
	statuses := collectStreamingStatuses(events)
	if len(statuses) != 2 {
		t.Fatalf("status event count = %d, want 2", len(statuses))
	}
	if statuses[0].Status.State != a2a.TaskStateWorking || statuses[1].Status.State != a2a.TaskStateCompleted {
		t.Fatalf("unexpected status sequence: %q, %q", statuses[0].Status.State, statuses[1].Status.State)
	}
	if len(collectStreamingArtifacts(events)) != 0 {
		t.Fatalf("artifact event count = %d, want 0", len(collectStreamingArtifacts(events)))
	}
}

func TestRequestHandler_OnCancelTask_ReturnsCanceledTask(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var runs sync.WaitGroup
	defer func() {
		close(release)
		runs.Wait()
	}()

	a := newHostedTestAgent(func(runCtx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			runs.Add(1)
			defer runs.Done()
			close(started)
			if !yield(&agent.ResponseUpdate{
				MessageID:         "m-cancel",
				Role:              message.RoleAssistant,
				Contents:          message.Contents{&message.TextContent{Text: "working"}},
				ContinuationToken: "token-cancel",
			}, nil) {
				return
			}
			select {
			case <-runCtx.Done():
				yield(nil, runCtx.Err())
			case <-release:
			}
		}
	})
	h := newRequestHandler(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})

	result, err := h.SendMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
		Config:  &a2a.SendMessageConfig{ReturnImmediately: true},
	})
	if err != nil {
		t.Fatalf("OnSendMessage returned error: %v", err)
	}
	task, ok := result.(*a2a.Task)
	if !ok {
		t.Fatalf("result type = %T, want *a2a.Task", result)
	}
	if task.ID == "" {
		t.Fatal("expected task id")
	}
	if task.Status.State.Terminal() {
		t.Fatalf("task status = %q, want an in-progress task", task.Status.State)
	}
	<-started

	canceled, err := h.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: task.ID})
	if err != nil {
		t.Fatalf("OnCancelTask returned error: %v", err)
	}
	runs.Wait()
	if canceled == nil {
		t.Fatal("CancelTask returned nil task")
	}
	if canceled.Status.State != a2a.TaskStateCanceled {
		t.Fatalf("task status = %q, want %q", canceled.Status.State, a2a.TaskStateCanceled)
	}
	if canceled.ID != task.ID || canceled.ContextID != task.ContextID {
		t.Errorf("canceled task identity = (%q, %q), want (%q, %q)", canceled.ID, canceled.ContextID, task.ID, task.ContextID)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func collectFirstStreamingTask(t *testing.T, stream iter.Seq2[a2a.Event, error]) *a2a.Task {
	t.Helper()

	for evt, err := range stream {
		if err != nil {
			t.Fatalf("stream returned error: %v", err)
		}
		task, ok := evt.(*a2a.Task)
		if ok {
			return task
		}
	}
	t.Fatal("expected task event")
	return nil
}

func collectStreamingEvents(t *testing.T, stream iter.Seq2[a2a.Event, error]) []a2a.Event {
	t.Helper()
	events, err := collectStreamingEventsAndError(stream)
	if err != nil {
		t.Fatalf("stream returned error: %v", err)
	}
	return events
}

func collectStreamingEventsAndError(stream iter.Seq2[a2a.Event, error]) ([]a2a.Event, error) {
	var events []a2a.Event
	for evt, err := range stream {
		if err != nil {
			return events, err
		}
		if evt == nil {
			continue
		}
		events = append(events, evt)
	}
	return events, nil
}

func collectStreamingArtifacts(events []a2a.Event) []*a2a.TaskArtifactUpdateEvent {
	artifacts := make([]*a2a.TaskArtifactUpdateEvent, 0)
	for _, evt := range events {
		artifact, ok := evt.(*a2a.TaskArtifactUpdateEvent)
		if ok {
			artifacts = append(artifacts, artifact)
		}
	}
	return artifacts
}

func collectStreamingStatuses(events []a2a.Event) []*a2a.TaskStatusUpdateEvent {
	statuses := make([]*a2a.TaskStatusUpdateEvent, 0)
	for _, evt := range events {
		status, ok := evt.(*a2a.TaskStatusUpdateEvent)
		if ok {
			statuses = append(statuses, status)
		}
	}
	return statuses
}

func collectStreamingTasks(events []a2a.Event) []*a2a.Task {
	tasks := make([]*a2a.Task, 0)
	for _, evt := range events {
		task, ok := evt.(*a2a.Task)
		if ok {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func agentMessageTexts(messages []*message.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			texts = append(texts, "")
			continue
		}
		texts = append(texts, msg.String())
	}
	return texts
}

func a2aMessageTexts(messages []*a2a.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, msg := range messages {
		texts = append(texts, a2aMessageText(msg))
	}
	return texts
}

func a2aMessageText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range msg.Parts {
		if part == nil {
			continue
		}
		sb.WriteString(part.Text())
	}
	return sb.String()
}

func a2aArtifactText(evt *a2a.TaskArtifactUpdateEvent) string {
	if evt == nil || evt.Artifact == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range evt.Artifact.Parts {
		if part == nil {
			continue
		}
		sb.WriteString(part.Text())
	}
	return sb.String()
}

// directExecutorHandler supplies only the public SDK's streaming call context.
// It forwards to Execute without request processing, task storage, or event assembly.
type directExecutorHandler struct {
	a2asrv.RequestHandler
	executor a2asrv.AgentExecutor
	execCtx  *a2asrv.ExecutorContext
}

func (h directExecutorHandler) SendStreamingMessage(ctx context.Context, _ *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return h.executor.Execute(ctx, h.execCtx)
}

func directExecutorStream(ctx context.Context, executor a2asrv.AgentExecutor, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	h := &a2asrv.InterceptedHandler{Handler: directExecutorHandler{executor: executor, execCtx: execCtx}}
	return h.SendStreamingMessage(ctx, &a2a.SendMessageRequest{Message: execCtx.Message})
}

func hostedExecutorContext(taskID a2a.TaskID, contextID string) *a2asrv.ExecutorContext {
	return &a2asrv.ExecutorContext{
		TaskID:    taskID,
		ContextID: contextID,
		Message: &a2a.Message{
			ID:    "test-id",
			Role:  a2a.MessageRoleUser,
			Parts: a2a.ContentParts{a2a.NewTextPart("Hello")},
		},
	}
}

func hostedContinuationContext() *a2asrv.ExecutorContext {
	execCtx := hostedExecutorContext("task-1", "ctx-1")
	execCtx.Message = &a2a.Message{ID: "empty", Role: a2a.MessageRoleUser}
	execCtx.StoredTask = &a2a.Task{
		ID:        "task-1",
		ContextID: "ctx-1",
		History:   []*a2a.Message{{Role: a2a.MessageRoleUser, Parts: a2a.ContentParts{a2a.NewTextPart("Hello")}}},
	}
	return execCtx
}

func hostedReply(text string) *agent.Response {
	return &agent.Response{Messages: []*message.Message{{
		Role:     message.RoleAssistant,
		Contents: message.Contents{&message.TextContent{Text: text}},
	}}}
}

func hostedUpdate(messageID, text string) *agent.ResponseUpdate {
	update := &agent.ResponseUpdate{Role: message.RoleAssistant, ResponseID: "r1", MessageID: messageID}
	if text != "" {
		update.Contents = message.Contents{&message.TextContent{Text: text}}
	}
	return update
}

func hostedResponseExecutor(response *agent.Response, cfg a2aprovider.ExecutorConfig) a2asrv.AgentExecutor {
	return a2aprovider.NewExecutor(newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, update := range response.ToUpdates() {
				if !yield(update, nil) {
					return
				}
			}
		}
	}), cfg)
}

func hostedStreamingExecutor(updates []*agent.ResponseUpdate, finalErr error) a2asrv.AgentExecutor {
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			stream, _ := agent.GetOption(options, agent.Stream)
			if !stream {
				yield(nil, assertErr("non-streaming provider was not configured"))
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
	return a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{AgentRunMode: a2aprovider.ReturnTask()})
}

func singleHostedMessage(t *testing.T, events []a2a.Event) *a2a.Message {
	t.Helper()
	var messages []*a2a.Message
	for _, event := range events {
		if msg, ok := event.(*a2a.Message); ok {
			messages = append(messages, msg)
		}
	}
	if len(messages) != 1 {
		t.Fatalf("message events = %d, want 1", len(messages))
	}
	return messages[0]
}

func assertHostedArtifact(t *testing.T, artifact *a2a.TaskArtifactUpdateEvent, text string, id a2a.ArtifactID, appendChunk, lastChunk bool) {
	t.Helper()
	if artifact.Artifact == nil || len(artifact.Artifact.Parts) != 1 {
		t.Fatalf("artifact = %#v, want one part", artifact.Artifact)
	}
	if got := artifact.Artifact.Parts[0].Text(); got != text {
		t.Errorf("artifact text = %q, want %q", got, text)
	}
	if id != "" && artifact.Artifact.ID != id {
		t.Errorf("artifact ID = %q, want %q", artifact.Artifact.ID, id)
	}
	if artifact.Append != appendChunk || artifact.LastChunk != lastChunk {
		t.Errorf("artifact flags = (%t, %t), want (%t, %t)", artifact.Append, artifact.LastChunk, appendChunk, lastChunk)
	}
}

func assertHostedStatuses(t *testing.T, events []a2a.Event, states ...a2a.TaskState) []*a2a.TaskStatusUpdateEvent {
	t.Helper()
	statuses := collectStreamingStatuses(events)
	if len(statuses) != len(states) {
		t.Fatalf("status events = %d, want %d: %#v", len(statuses), len(states), statuses)
	}
	for i, state := range states {
		if statuses[i].Status.State != state {
			t.Errorf("status %d = %q, want %q", i, statuses[i].Status.State, state)
		}
	}
	return statuses
}

func TestExecutor_ResponseMetadata(t *testing.T) {
	response := hostedReply("Test response")
	response.AdditionalProperties = map[string]any{"responseKey1": "responseValue1", "responseKey2": 123}
	e := hostedResponseExecutor(response, a2aprovider.ExecutorConfig{})
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "ctx"))))
	if len(msg.Metadata) != 2 {
		t.Fatalf("metadata = %#v, want two properties", msg.Metadata)
	}
	for _, key := range []string{"responseKey1", "responseKey2"} {
		if _, ok := msg.Metadata[key]; !ok {
			t.Errorf("metadata is missing %q", key)
		}
	}
}

func TestExecutor_NilResponseMetadata(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Test response"), a2aprovider.ExecutorConfig{})
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "ctx"))))
	if msg.Metadata != nil {
		t.Errorf("metadata = %#v, want nil", msg.Metadata)
	}
}

func TestExecutor_EmptyResponseMetadata(t *testing.T) {
	response := hostedReply("Test response")
	response.AdditionalProperties = map[string]any{}
	e := hostedResponseExecutor(response, a2aprovider.ExecutorConfig{})
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "ctx"))))
	if msg.Metadata != nil {
		t.Errorf("metadata = %#v, want nil", msg.Metadata)
	}
}

func TestExecutor_ProvidedContextID(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Reply"), a2aprovider.ExecutorConfig{})
	execCtx := hostedExecutorContext("", "my-context-123")
	execCtx.Message.ContextID = "my-context-123"
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), execCtx)))
	if msg.ContextID != "my-context-123" {
		t.Errorf("context ID = %q, want my-context-123", msg.ContextID)
	}
}

func TestExecutor_ContinuationCompletes(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Done!"), a2aprovider.ExecutorConfig{})
	events := collectStreamingEvents(t, e.Execute(t.Context(), hostedContinuationContext()))
	if len(collectStreamingArtifacts(events)) == 0 || len(collectStreamingStatuses(events)) == 0 {
		t.Errorf("events = %#v, want artifact and status events", events)
	}
	for _, event := range events {
		if _, ok := event.(*a2a.Message); ok {
			t.Error("unexpected message event")
		}
	}
}

func hostedFailingExecutor(err error) a2asrv.AgentExecutor {
	a := newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(nil, err) }
	})
	return a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{})
}

func TestExecutor_ContinuationFailureEmitsStatus(t *testing.T) {
	wantErr := assertErr("Agent failed")
	events, err := collectStreamingEventsAndError(hostedFailingExecutor(wantErr).Execute(t.Context(), hostedContinuationContext()))
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
	if len(collectStreamingStatuses(events)) == 0 {
		t.Error("expected status event")
	}
}

func TestExecutor_ContinuationFailureWithCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	wantErr := assertErr("Agent failed")
	a := newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(nil, wantErr) }
	})
	store := &hostedSessionStore{session: new(agent.Session)}
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{SessionStore: store})
	events, err := collectStreamingEventsAndError(e.Execute(ctx, hostedContinuationContext()))
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want original %v", err, wantErr)
	}
	if len(collectStreamingStatuses(events)) == 0 {
		t.Error("expected status event even with canceled context")
	}
}

func TestExecutor_ContinuationCancellationDoesNotEmitStatus(t *testing.T) {
	events, err := collectStreamingEventsAndError(hostedFailingExecutor(context.Canceled).Execute(t.Context(), hostedContinuationContext()))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if statuses := collectStreamingStatuses(events); len(statuses) != 0 {
		t.Errorf("status events = %d, want 0", len(statuses))
	}
}

func TestExecutor_ReferenceTasksRejected(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Test response"), a2aprovider.ExecutorConfig{})
	execCtx := hostedExecutorContext("", "ctx")
	execCtx.Message.ReferenceTasks = []a2a.TaskID{"other-task-id"}
	_, err := collectStreamingEventsAndError(e.Execute(t.Context(), execCtx))
	if err == nil || !strings.Contains(err.Error(), "referenceTaskIds is not supported") {
		t.Errorf("error = %v, want unsupported reference tasks", err)
	}
}

func TestExecutor_MissingContextIDGeneratesID(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Reply"), a2aprovider.ExecutorConfig{})
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", ""))))
	if msg.ContextID == "" {
		t.Error("expected generated context ID")
	}
}

func TestExecutor_NilMessageUsesEmptyInput(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Reply"), a2aprovider.ExecutorConfig{})
	execCtx := hostedExecutorContext("", "ctx")
	execCtx.Message = nil
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), execCtx)))
	if msg.ContextID != "ctx" {
		t.Errorf("context ID = %q, want ctx", msg.ContextID)
	}
}

func TestExecutor_CancelEmitsStatus(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Test response"), a2aprovider.ExecutorConfig{})
	execCtx := hostedContinuationContext()
	execCtx.StoredTask.History = nil
	events := collectStreamingEvents(t, e.Cancel(t.Context(), execCtx))
	if len(collectStreamingStatuses(events)) == 0 {
		t.Error("expected status event")
	}
}

func TestExecutor_StreamMissingMessageIDContinuesArtifact(t *testing.T) {
	e := hostedStreamingExecutor([]*agent.ResponseUpdate{
		hostedUpdate("m1", "m1 chunk 1"), hostedUpdate("", "m1 chunk 2"),
		hostedUpdate("m2", "m2 chunk 1"), hostedUpdate("", "m2 chunk 2"),
	}, nil)
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 4 {
		t.Fatalf("artifact events = %d, want 4", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "m1 chunk 1", "m1", false, false)
	assertHostedArtifact(t, artifacts[1], "m1 chunk 2", "m1", true, true)
	assertHostedArtifact(t, artifacts[2], "m2 chunk 1", "m2", false, false)
	assertHostedArtifact(t, artifacts[3], "m2 chunk 2", "m2", true, true)
}

func TestExecutor_StreamWithoutMessageIDs(t *testing.T) {
	e := hostedStreamingExecutor([]*agent.ResponseUpdate{hostedUpdate("", "chunk 1"), hostedUpdate("", "chunk 2")}, nil)
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 2 {
		t.Fatalf("artifact events = %d, want 2", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "chunk 1", "", false, false)
	if artifacts[1].Artifact.ID != artifacts[0].Artifact.ID {
		t.Error("artifact ID changed between chunks")
	}
	assertHostedArtifact(t, artifacts[1], "chunk 2", "", true, true)
}

func TestExecutor_StreamEmptyMessageIDs(t *testing.T) {
	// Go's empty string represents both absent and explicitly empty message IDs.
	e := hostedStreamingExecutor([]*agent.ResponseUpdate{hostedUpdate("", "chunk 1"), hostedUpdate("", "chunk 2")}, nil)
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 2 {
		t.Fatalf("artifact events = %d, want 2", len(artifacts))
	}
	if artifacts[0].Artifact.ID == "" {
		t.Error("expected nonempty artifact ID")
	}
	assertHostedArtifact(t, artifacts[0], "chunk 1", "", false, false)
	assertHostedArtifact(t, artifacts[1], "chunk 2", artifacts[0].Artifact.ID, true, true)
}

func TestExecutor_StreamReappearingMessageID(t *testing.T) {
	e := hostedStreamingExecutor([]*agent.ResponseUpdate{
		hostedUpdate("m1", "first m1"), hostedUpdate("m2", "m2"),
		hostedUpdate("m1", "second m1 chunk 1"), hostedUpdate("m1", "second m1 chunk 2"),
	}, nil)
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 4 {
		t.Fatalf("artifact events = %d, want 4", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "first m1", "m1", false, true)
	assertHostedArtifact(t, artifacts[1], "m2", "m2", false, true)
	if artifacts[2].Artifact.ID == "m1" || artifacts[2].Artifact.ID == "m2" {
		t.Errorf("reappearing message artifact ID = %q, want distinct ID", artifacts[2].Artifact.ID)
	}
	assertHostedArtifact(t, artifacts[2], "second m1 chunk 1", "", false, false)
	assertHostedArtifact(t, artifacts[3], "second m1 chunk 2", artifacts[2].Artifact.ID, true, true)
}

func TestExecutor_StreamNoUpdatesCompletes(t *testing.T) {
	e := hostedStreamingExecutor(nil, nil)
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	for _, event := range events {
		if _, ok := event.(*a2a.Message); ok {
			t.Error("unexpected message event")
		}
	}
	if artifacts := collectStreamingArtifacts(events); len(artifacts) != 0 {
		t.Errorf("artifact events = %d, want 0", len(artifacts))
	}
	tasks := collectStreamingTasks(events)
	if len(tasks) != 1 || tasks[0].Status.State != a2a.TaskStateSubmitted {
		t.Errorf("task events = %#v, want one submitted task", tasks)
	}
	assertHostedStatuses(t, events, a2a.TaskStateWorking, a2a.TaskStateCompleted)
}

func TestExecutor_StreamContentlessUpdatesComplete(t *testing.T) {
	e := hostedStreamingExecutor([]*agent.ResponseUpdate{hostedUpdate("m1", ""), hostedUpdate("m1", "")}, nil)
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	if artifacts := collectStreamingArtifacts(events); len(artifacts) != 0 {
		t.Errorf("artifact events = %d, want 0", len(artifacts))
	}
	statuses := collectStreamingStatuses(events)
	if len(statuses) == 0 || statuses[len(statuses)-1].Status.State != a2a.TaskStateCompleted {
		t.Errorf("statuses = %#v, want final completed state", statuses)
	}
}

func TestExecutor_StreamContentlessMessageBoundary(t *testing.T) {
	e := hostedStreamingExecutor([]*agent.ResponseUpdate{
		hostedUpdate("m1", "m1 chunk"), hostedUpdate("m2", ""), hostedUpdate("m2", "m2 chunk"),
	}, nil)
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("task-1", "ctx")))
	artifacts := collectStreamingArtifacts(events)
	if len(artifacts) != 2 {
		t.Fatalf("artifact events = %d, want 2", len(artifacts))
	}
	assertHostedArtifact(t, artifacts[0], "m1 chunk", "m1", false, true)
	assertHostedArtifact(t, artifacts[1], "m2 chunk", "m2", false, true)
}

func TestExecutor_StreamRequestMetadata(t *testing.T) {
	var captured []agent.Option
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = options
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("", "reply"), nil) }
	})
	execCtx := hostedExecutorContext("", "ctx")
	execCtx.Metadata = map[string]any{"key1": "value1"}
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{})
	collectStreamingEvents(t, directExecutorStream(t.Context(), e, execCtx))
	if captured == nil {
		t.Fatal("provider options were not captured")
	}
	metadata, _ := agent.GetOption(captured, a2aprovider.WithMetadata)
	if metadata == nil || metadata["key1"] != "value1" {
		t.Errorf("metadata = %#v, want key1=value1", metadata)
	}
}

func TestExecutor_StreamReferenceTasksRejected(t *testing.T) {
	e := hostedStreamingExecutor(nil, nil)
	execCtx := hostedExecutorContext("", "ctx")
	execCtx.Message.ReferenceTasks = []a2a.TaskID{"other-task-id"}
	_, err := collectStreamingEventsAndError(directExecutorStream(t.Context(), e, execCtx))
	if err == nil || !strings.Contains(err.Error(), "referenceTaskIds is not supported") {
		t.Errorf("error = %v, want unsupported reference tasks", err)
	}
}

func TestExecutor_StreamPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var captured context.Context
	a := newHostedTestAgent(func(ctx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		captured = ctx
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("", "reply"), nil) }
	})
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{})
	collectStreamingEvents(t, directExecutorStream(ctx, e, hostedExecutorContext("", "ctx")))
	if captured == nil || captured.Done() != ctx.Done() {
		t.Error("provider did not receive the caller's cancellation signal")
	}
}

type hostedSessionSave struct {
	ctx       context.Context
	contextID string
	session   *agent.Session
}

type hostedSessionStore struct {
	session *agent.Session
	getKeys []string
	saves   []hostedSessionSave
	getErr  error
	saveErr error
}

func (s *hostedSessionStore) Get(_ context.Context, contextID string) (*agent.Session, error) {
	s.getKeys = append(s.getKeys, contextID)
	return s.session, s.getErr
}

func (s *hostedSessionStore) Save(ctx context.Context, contextID string, session *agent.Session) error {
	s.saves = append(s.saves, hostedSessionSave{ctx: ctx, contextID: contextID, session: session})
	return s.saveErr
}

func hostedSessionExecutor(store a2aprovider.SessionStore, updates []*agent.ResponseUpdate, runErr error) a2asrv.AgentExecutor {
	a := newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for _, update := range updates {
				if !yield(update, nil) {
					return
				}
			}
			if runErr != nil {
				yield(nil, runErr)
			}
		}
	})
	return a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{SessionStore: store})
}

func assertHostedSessionSaved(t *testing.T, store *hostedSessionStore, contextID string, withoutCancel bool) {
	t.Helper()
	if len(store.saves) != 1 {
		t.Fatalf("session saves = %d, want 1", len(store.saves))
	}
	saved := store.saves[0]
	if saved.contextID != contextID {
		t.Errorf("saved context ID = %q, want %q", saved.contextID, contextID)
	}
	if saved.session == nil {
		t.Error("saved session is nil")
	}
	if withoutCancel {
		if saved.ctx.Done() != nil || saved.ctx.Err() != nil {
			t.Error("session save received a cancelable context")
		}
		if _, ok := saved.ctx.Deadline(); ok {
			t.Error("session save received a deadline")
		}
	}
}

func hostedSessionContinuationContext() *a2asrv.ExecutorContext {
	execCtx := hostedContinuationContext()
	execCtx.ContextID = "ctx-cont"
	execCtx.StoredTask.ContextID = "ctx-cont"
	return execCtx
}

func TestExecutor_StreamSavesSessionAfterProcessing(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	e := hostedSessionExecutor(store, []*agent.ResponseUpdate{hostedUpdate("", "chunk")}, nil)
	collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx-stream")))
	assertHostedSessionSaved(t, store, "ctx-stream", false)
}

func TestExecutor_StreamNoUpdatesEmitsEmptyMessageAndSavesSession(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	e := hostedSessionExecutor(store, nil, nil)
	events := collectStreamingEvents(t, directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx")))
	msg := singleHostedMessage(t, events)
	if len(msg.Parts) != 0 {
		t.Errorf("message parts = %#v, want empty", msg.Parts)
	}
	assertHostedSessionSaved(t, store, "ctx", false)
}

func TestExecutor_NilSessionStoreExecutesSuccessfully(t *testing.T) {
	e := hostedResponseExecutor(hostedReply("Reply"), a2aprovider.ExecutorConfig{SessionStore: nil})
	events := collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "ctx-1")))
	msg := singleHostedMessage(t, events)
	if len(msg.Parts) == 0 || msg.Parts[0].Text() != "Reply" {
		t.Errorf("message parts = %#v, want Reply", msg.Parts)
	}
}

func TestExecutor_CustomSessionStore(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	e := hostedSessionExecutor(store, hostedReply("Reply").ToUpdates(), nil)
	collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "ctx-1")))
	if !slices.Equal(store.getKeys, []string{"ctx-1"}) {
		t.Errorf("session lookups = %v, want [ctx-1]", store.getKeys)
	}
	assertHostedSessionSaved(t, store, "ctx-1", false)
}

func TestExecutor_NilSessionStorePersistsAcrossCalls(t *testing.T) {
	var creates int
	a := agent.New(agent.ProviderConfig{
		CreateSession: func(context.Context, *agent.Session, ...agent.Option) error {
			creates++
			return nil
		},
		Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("", "Reply"), nil) }
		},
	}, agent.Config{Name: "test-agent"})
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{SessionStore: nil})
	execCtx := hostedExecutorContext("", "ctx-persistent")
	collectStreamingEvents(t, e.Execute(t.Context(), execCtx))
	collectStreamingEvents(t, e.Execute(t.Context(), execCtx))
	if creates != 1 {
		t.Errorf("session creations = %d, want 1", creates)
	}
}

func TestExecutor_NonStreamingFailureSavesSessionWithoutCancellation(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	wantErr := assertErr("Agent failed")
	e := hostedSessionExecutor(store, nil, wantErr)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := collectStreamingEventsAndError(e.Execute(ctx, hostedExecutorContext("", "ctx")))
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
	assertHostedSessionSaved(t, store, "ctx", true)
}

func TestExecutor_StreamFailureSavesSessionWithoutCancellation(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	wantErr := assertErr("Stream failed")
	e := hostedSessionExecutor(store, nil, wantErr)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := collectStreamingEventsAndError(directExecutorStream(ctx, e, hostedExecutorContext("", "ctx-stream")))
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
	assertHostedSessionSaved(t, store, "ctx-stream", true)
}

func TestExecutor_ContinuationFailureSavesSessionWithoutCancellation(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	wantErr := assertErr("Agent failed")
	e := hostedSessionExecutor(store, nil, wantErr)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := collectStreamingEventsAndError(e.Execute(ctx, hostedSessionContinuationContext()))
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
	assertHostedSessionSaved(t, store, "ctx-cont", true)
}

func TestExecutor_NonStreamingSavesSessionWithoutCancellation(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	e := hostedSessionExecutor(store, hostedReply("Reply").ToUpdates(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	collectStreamingEvents(t, e.Execute(ctx, hostedExecutorContext("", "ctx")))
	assertHostedSessionSaved(t, store, "ctx", true)
}

func TestExecutor_StreamSavesSessionWithoutCancellation(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	e := hostedSessionExecutor(store, []*agent.ResponseUpdate{hostedUpdate("", "chunk")}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	collectStreamingEvents(t, directExecutorStream(ctx, e, hostedExecutorContext("", "ctx-stream")))
	assertHostedSessionSaved(t, store, "ctx-stream", true)
}

func TestExecutor_ContinuationSavesSessionWithoutCancellation(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	e := hostedSessionExecutor(store, hostedReply("Done!").ToUpdates(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	collectStreamingEvents(t, e.Execute(ctx, hostedSessionContinuationContext()))
	assertHostedSessionSaved(t, store, "ctx-cont", true)
}

func TestInMemorySessionStore_Missing(t *testing.T) {
	session, err := a2aprovider.NewInMemorySessionStore().Get(t.Context(), "missing")
	if err != nil || session != nil {
		t.Fatalf("Get = (%v, %v), want (nil, nil)", session, err)
	}
}

func TestInMemorySessionStore_IndependentSnapshots(t *testing.T) {
	store := a2aprovider.NewInMemorySessionStore()
	original := new(agent.Session)
	original.SetServiceID("provider-session")
	state := map[string][]string{"items": {"original"}}
	original.Set("state", state)
	if err := store.Save(t.Context(), "ctx", original); err != nil {
		t.Fatal(err)
	}
	state["items"][0] = "mutated after save"
	original.SetServiceID("changed after save")
	first, err := store.Get(t.Context(), "ctx")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Get(t.Context(), "ctx")
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil {
		t.Fatal("saved session was not retrieved")
	}
	var firstState, secondState map[string][]string
	if ok, err := first.Get("state", &firstState); !ok || err != nil {
		t.Fatalf("first state = (%t, %v), want present", ok, err)
	}
	firstState["items"][0] = "first branch"
	first.Set("state", firstState)
	first.SetServiceID("first-provider-session")
	if ok, err := second.Get("state", &secondState); !ok || err != nil {
		t.Fatalf("second state = (%t, %v), want present", ok, err)
	}
	if !slices.Equal(secondState["items"], []string{"original"}) || second.ServiceID() != "provider-session" {
		t.Fatalf("second branch = (%v, %q), want original snapshot", secondState, second.ServiceID())
	}
	if err := store.Save(t.Context(), "branch", first); err != nil {
		t.Fatal(err)
	}
	firstState["items"][0] = "mutated branch after save"
	branch, err := store.Get(t.Context(), "branch")
	if err != nil || branch == nil {
		t.Fatalf("branch Get = (%v, %v)", branch, err)
	}
	var branchState map[string][]string
	if ok, err := branch.Get("state", &branchState); !ok || err != nil {
		t.Fatalf("branch state = (%t, %v), want present", ok, err)
	}
	if !slices.Equal(branchState["items"], []string{"first branch"}) || branch.ServiceID() != "first-provider-session" {
		t.Errorf("saved branch = (%v, %q), want first branch snapshot", branchState, branch.ServiceID())
	}
}

func TestInMemorySessionStore_RetainsLazyHistory(t *testing.T) {
	var inputs [][]string
	a := newHostedTestAgent(func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		inputs = append(inputs, agentMessageTexts(messages))
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("", "Reply"), nil) }
	})
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Run(t.Context(), []*message.Message{message.NewText("Hello")}, agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	store := a2aprovider.NewInMemorySessionStore()
	if err := store.Save(t.Context(), "ctx", session); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(t.Context(), "ctx")
	if err != nil || loaded == nil {
		t.Fatalf("Get = (%v, %v)", loaded, err)
	}
	// Save again without reading private history to exercise lazy session state.
	if err := store.Save(t.Context(), "ctx", loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Get(t.Context(), "ctx")
	if err != nil || loaded == nil {
		t.Fatalf("second Get = (%v, %v)", loaded, err)
	}
	if _, err := a.Run(t.Context(), []*message.Message{message.NewText("again")}, agent.WithSession(loaded)).Collect(); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || !slices.Equal(inputs[1], []string{"Hello", "Reply", "again"}) {
		t.Errorf("provider inputs = %v, want retained history on second run", inputs)
	}
}

func TestInMemorySessionStore_SaveFailurePreservesSnapshot(t *testing.T) {
	store := a2aprovider.NewInMemorySessionStore()
	session := new(agent.Session)
	session.Set("value", "saved")
	if err := store.Save(t.Context(), "ctx", session); err != nil {
		t.Fatal(err)
	}
	session.Set("value", "not saved")
	session.Set("unsupported", make(chan int))
	var unsupported *json.UnsupportedTypeError
	if err := store.Save(t.Context(), "ctx", session); !errors.As(err, &unsupported) {
		t.Fatalf("Save error = %v, want JSON unsupported type", err)
	}
	loaded, err := store.Get(t.Context(), "ctx")
	if err != nil || loaded == nil {
		t.Fatalf("Get = (%v, %v)", loaded, err)
	}
	var value string
	if ok, err := loaded.Get("value", &value); !ok || err != nil || value != "saved" {
		t.Errorf("stored value = (%q, %t, %v), want saved", value, ok, err)
	}
}

func TestInMemorySessionStore_ConcurrentSnapshots(t *testing.T) {
	store := a2aprovider.NewInMemorySessionStore()
	seed := new(agent.Session)
	seed.Set("value", "shared seed")
	if err := store.Save(t.Context(), "shared", seed); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range 16 {
		workers.Go(func() {
			<-start
			session, err := store.Get(t.Context(), "shared")
			if err != nil || session == nil {
				t.Errorf("Get = (%v, %v)", session, err)
				return
			}
			key := fmt.Sprintf("ctx-%d", i)
			session.Set("value", key)
			if err := store.Save(t.Context(), key, session); err != nil {
				t.Error(err)
				return
			}
			// Different owned sessions may also save to the same key concurrently.
			if err := store.Save(t.Context(), "shared", session); err != nil {
				t.Error(err)
			}
			loaded, err := store.Get(t.Context(), key)
			if err != nil || loaded == nil {
				t.Errorf("Get(%q) = (%v, %v)", key, loaded, err)
				return
			}
			var value string
			if ok, err := loaded.Get("value", &value); !ok || err != nil || value != key {
				t.Errorf("Get(%q) state = (%q, %t, %v)", key, value, ok, err)
			}
		})
	}
	close(start)
	workers.Wait()
}

func TestInMemorySessionStore_Cancellation(t *testing.T) {
	store := a2aprovider.NewInMemorySessionStore()
	session := new(agent.Session)
	session.Set("value", "saved")
	if err := store.Save(t.Context(), "ctx", session); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if loaded, err := store.Get(ctx, "ctx"); loaded != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Get = (%v, %v), want (nil, context.Canceled)", loaded, err)
	}
	session.Set("value", "not saved")
	if err := store.Save(ctx, "ctx", session); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Save error = %v, want context.Canceled", err)
	}
	loaded, err := store.Get(t.Context(), "ctx")
	if err != nil || loaded == nil {
		t.Fatalf("Get = (%v, %v)", loaded, err)
	}
	var value string
	if ok, err := loaded.Get("value", &value); !ok || err != nil || value != "saved" {
		t.Errorf("stored value = (%q, %t, %v), want saved", value, ok, err)
	}
}

func TestInMemorySessionStore_NilSession(t *testing.T) {
	store := a2aprovider.NewInMemorySessionStore()
	if err := store.Save(t.Context(), "ctx", nil); err == nil {
		t.Fatal("Save accepted a nil session")
	}
	if session, err := store.Get(t.Context(), "ctx"); session != nil || err != nil {
		t.Errorf("Get after failed Save = (%v, %v), want (nil, nil)", session, err)
	}
}

func TestExecutor_DefaultSessionStoreContextIsolation(t *testing.T) {
	var inputs [][]string
	a := newHostedTestAgent(func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		inputs = append(inputs, agentMessageTexts(messages))
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("", "Reply"), nil) }
	})
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{})
	for _, contextID := range []string{"first", "second", "first"} {
		collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", contextID)))
	}
	if len(inputs) != 3 {
		t.Fatalf("provider calls = %d, want 3", len(inputs))
	}
	for i, want := range [][]string{{"Hello"}, {"Hello"}, {"Hello", "Reply", "Hello"}} {
		if !slices.Equal(inputs[i], want) {
			t.Errorf("call %d input = %v, want %v", i, inputs[i], want)
		}
	}
}

func TestExecutor_DefaultSessionStoresAreIndependent(t *testing.T) {
	var inputs [][]string
	a := newHostedTestAgent(func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		inputs = append(inputs, agentMessageTexts(messages))
		return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("", "Reply"), nil) }
	})
	first := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{})
	second := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{})
	collectStreamingEvents(t, first.Execute(t.Context(), hostedExecutorContext("", "ctx")))
	collectStreamingEvents(t, second.Execute(t.Context(), hostedExecutorContext("", "ctx")))
	if len(inputs) != 2 || !slices.Equal(inputs[1], []string{"Hello"}) {
		t.Errorf("provider inputs = %v, want no history from the first executor", inputs)
	}
}

func TestExecutor_SessionStoreGetFailure(t *testing.T) {
	wantErr := assertErr("load failed")
	store := &hostedSessionStore{getErr: wantErr}
	var ran bool
	a := newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		ran = true
		return func(func(*agent.ResponseUpdate, error) bool) {}
	})
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{SessionStore: store})
	_, err := collectStreamingEventsAndError(e.Execute(t.Context(), hostedExecutorContext("", "ctx")))
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
	if ran || len(store.saves) != 0 {
		t.Errorf("after failed Get: ran=%t, saves=%d, want neither", ran, len(store.saves))
	}
}

func TestExecutor_DynamicDecisionFailureDoesNotSaveSession(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, restored := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/restored=%t", stream, restored), func(t *testing.T) {
				store := &hostedSessionStore{saveErr: assertErr("save failed")}
				if restored {
					store.session = new(agent.Session)
				}
				var invoked bool
				a := newHostedTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
					invoked = true
					return func(func(*agent.ResponseUpdate, error) bool) {}
				})
				wantErr := assertErr("decision failed")
				e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{
					SessionStore: store,
					AgentRunMode: a2aprovider.ReturnTaskWhen(func(context.Context, *a2asrv.ExecutorContext) (bool, error) {
						return false, wantErr
					}),
				})
				request := hostedExecutorContext("", "ctx")
				seq := e.Execute(t.Context(), request)
				if stream {
					seq = directExecutorStream(t.Context(), e, request)
				}
				events, err := collectStreamingEventsAndError(seq)
				if !errors.Is(err, wantErr) || invoked || len(events) != 0 {
					t.Fatalf("events = %#v, error = %v, invoked = %t; want only the decision error", events, err, invoked)
				}
				if len(store.saves) != 0 {
					t.Errorf("session saves = %d, want none before agent execution", len(store.saves))
				}
			})
		}
	}
}

func TestExecutor_DynamicDecisionFailureRetryCreatesSession(t *testing.T) {
	store := a2aprovider.NewInMemorySessionStore()
	var creates int
	a := agent.New(agent.ProviderConfig{
		CreateSession: func(context.Context, *agent.Session, ...agent.Option) error {
			creates++
			return nil
		},
		Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) { yield(hostedUpdate("", "Reply"), nil) }
		},
	}, agent.Config{})
	wantErr := assertErr("decision failed")
	fail := true
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{
		SessionStore: store,
		AgentRunMode: a2aprovider.ReturnTaskWhen(func(context.Context, *a2asrv.ExecutorContext) (bool, error) {
			if fail {
				return false, wantErr
			}
			return false, nil
		}),
	})
	request := hostedExecutorContext("", "ctx")
	_, err := collectStreamingEventsAndError(e.Execute(t.Context(), request))
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if session, err := store.Get(t.Context(), "ctx"); err != nil || session != nil {
		t.Errorf("session after failed decision = (%v, %v), want (nil, nil)", session, err)
	}
	fail = false
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), request)))
	if len(msg.Parts) != 1 || msg.Parts[0].Text() != "Reply" || creates != 2 {
		t.Errorf("retry message = %#v, session creations = %d, want Reply and a fresh session", msg, creates)
	}
	if session, err := store.Get(t.Context(), "ctx"); err != nil || session == nil {
		t.Errorf("session after successful retry = (%v, %v), want persisted session", session, err)
	}
}

func TestExecutor_SessionSaveFailureIsReturned(t *testing.T) {
	wantErr := assertErr("save failed")
	store := &hostedSessionStore{session: new(agent.Session), saveErr: wantErr}
	e := hostedSessionExecutor(store, hostedReply("Reply").ToUpdates(), nil)
	_, err := collectStreamingEventsAndError(e.Execute(t.Context(), hostedExecutorContext("", "ctx")))
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
	assertHostedSessionSaved(t, store, "ctx", true)
}

func TestExecutor_CanceledRunSavesSessionWithContextValues(t *testing.T) {
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "trusted-value"))
	defer cancel()
	store := &hostedSessionStore{session: new(agent.Session)}
	a := newHostedTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			session, _ := agent.GetOption(options, agent.WithSession)
			session.Set("progress", "retained")
			cancel()
			yield(nil, context.Canceled)
		}
	})
	e := a2aprovider.NewExecutor(a, a2aprovider.ExecutorConfig{SessionStore: store})
	_, err := collectStreamingEventsAndError(e.Execute(ctx, hostedExecutorContext("", "ctx")))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	assertHostedSessionSaved(t, store, "ctx", true)
	if store.saves[0].ctx.Value(contextKey{}) != "trusted-value" {
		t.Error("save context lost trusted caller values")
	}
	var progress string
	if ok, err := store.saves[0].session.Get("progress", &progress); !ok || err != nil || progress != "retained" {
		t.Errorf("saved progress = (%q, %t, %v), want retained", progress, ok, err)
	}
}

func TestExecutor_SessionStoreUsesContextID(t *testing.T) {
	session := new(agent.Session)
	session.SetServiceID("provider-history")
	store := &hostedSessionStore{session: session}
	response := hostedReply("Reply")
	response.ConversationID = new("provider-history")
	e := hostedSessionExecutor(store, response.ToUpdates(), nil)
	collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", "a2a-context")))
	if !slices.Equal(store.getKeys, []string{"a2a-context"}) {
		t.Errorf("session lookups = %v, want [a2a-context]", store.getKeys)
	}
	assertHostedSessionSaved(t, store, "a2a-context", true)
}

func TestExecutor_SessionStoreUsesGeneratedContextID(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	e := hostedSessionExecutor(store, hostedReply("Reply").ToUpdates(), nil)
	msg := singleHostedMessage(t, collectStreamingEvents(t, e.Execute(t.Context(), hostedExecutorContext("", ""))))
	if msg.ContextID == "" {
		t.Fatal("expected generated context ID")
	}
	if !slices.Equal(store.getKeys, []string{msg.ContextID}) {
		t.Errorf("session lookups = %v, want [%s]", store.getKeys, msg.ContextID)
	}
	assertHostedSessionSaved(t, store, msg.ContextID, true)
}

func TestExecutor_SessionSaveFailurePreservesRunError(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			wantErr := assertErr("run failed")
			store := &hostedSessionStore{session: new(agent.Session), saveErr: assertErr("save failed")}
			e := hostedSessionExecutor(store, nil, wantErr)
			seq := e.Execute(t.Context(), hostedExecutorContext("", "ctx"))
			if stream {
				seq = directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx"))
			}
			_, err := collectStreamingEventsAndError(seq)
			if !errors.Is(err, wantErr) {
				t.Errorf("error = %v, want original %v", err, wantErr)
			}
			assertHostedSessionSaved(t, store, "ctx", true)
		})
	}
}

func TestExecutor_StreamEarlyStopSavesSession(t *testing.T) {
	store := &hostedSessionStore{session: new(agent.Session)}
	e := hostedSessionExecutor(store, []*agent.ResponseUpdate{hostedUpdate("", "Reply")}, nil)
	var received bool
	for event, err := range directExecutorStream(t.Context(), e, hostedExecutorContext("", "ctx")) {
		if err != nil {
			t.Fatal(err)
		}
		if event != nil {
			received = true
			break
		}
	}
	if !received {
		t.Fatal("no event was received")
	}
	assertHostedSessionSaved(t, store, "ctx", true)
}
