// Copyright (c) Microsoft. All rights reserved.

package a2ahosting_test

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/a2ahosting"
	"github.com/microsoft/agent-framework-go/message"
)

func newTestAgent(runFn func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*message.ResponseUpdate, error]) *agent.Agent {
	return agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{Name: "test-agent", ID: "test-agent-id"})
}

func TestNewRequestHandler_PanicsWithoutAgent(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when agent is nil")
		}
	}()
	_ = a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{})
}

func TestRequestHandler_OnSendMessage_ReturnsMessage_WhenBackgroundDisabled(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				MessageID: "m1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "hello from agent"}},
			}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
	result, err := h.SendMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	})
	if err != nil {
		t.Fatalf("OnSendMessage returned error: %v", err)
	}

	msg, ok := result.(*a2a.Message)
	if !ok {
		t.Fatalf("result type = %T, want *a2a.Message", result)
	}
	if msg.Role != a2a.MessageRoleAgent {
		t.Fatalf("role = %q, want %q", msg.Role, a2a.MessageRoleAgent)
	}
	if len(msg.Parts) == 0 {
		t.Fatal("expected at least one message part")
	}
	if got := msg.Parts[0].Text(); got != "hello from agent" {
		t.Fatalf("text = %q, want %q", got, "hello from agent")
	}
}

func TestRequestHandler_OnSendMessage_WithReferenceTaskIDs_ReturnsError(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: "ignored"}}}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
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

func TestRequestHandler_OnSendMessage_PreservesContextID(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				MessageID: "m-context",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "done"}},
			}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))
	msg.ContextID = "ctx-123"
	result, err := h.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("OnSendMessage returned error: %v", err)
	}
	msg, ok := result.(*a2a.Message)
	if !ok {
		t.Fatalf("result type = %T, want *a2a.Message", result)
	}
	if msg.ContextID != "ctx-123" {
		t.Fatalf("context id = %q, want %q", msg.ContextID, "ctx-123")
	}
}

func TestRequestHandler_OnSendMessageContinuation_UsesStoredTaskHistoryOnly(t *testing.T) {
	var callCount int
	var continuationInputs []string

	a := newTestAgent(func(_ context.Context, messagesIn []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		callCount++
		continuationInputs = agentMessageTexts(messagesIn)

		return func(yield func(*message.ResponseUpdate, error) bool) {
			if callCount != 1 {
				yield(nil, assertErr("unexpected agent invocation"))
				return
			}
			yield(&message.ResponseUpdate{
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

	h := a2ahosting.NewRequestHandler(
		a2ahosting.ExecutorConfig{Agent: a, AllowBackgroundResponses: true},
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

func TestRequestHandler_OnSendStreamingMessageContinuation_UsesTaskUpdatePath(t *testing.T) {
	var callCount int
	var continuationInputs []string
	var continuationStream bool

	a := newTestAgent(func(_ context.Context, messagesIn []*message.Message, options ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		callCount++
		continuationInputs = agentMessageTexts(messagesIn)
		continuationStream, _ = agent.GetOption(options, agent.Stream)

		return func(yield func(*message.ResponseUpdate, error) bool) {
			if callCount != 1 {
				yield(nil, assertErr("unexpected agent invocation"))
				return
			}
			yield(&message.ResponseUpdate{
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

	h := a2ahosting.NewRequestHandler(
		a2ahosting.ExecutorConfig{Agent: a, AllowBackgroundResponses: true},
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
	a := newTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		stream, _ := agent.GetOption(options, agent.Stream)
		if !stream {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, assertErr("expected Stream=true"))
			}
		}
		return func(yield func(*message.ResponseUpdate, error) bool) {
			if !yield(&message.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "chunk 1"}},
			}, nil) {
				return
			}
			yield(&message.ResponseUpdate{
				ResponseID: "r2",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "chunk 2"}},
			}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
	events := collectStreamingEvents(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping")),
	}))

	if len(collectStreamingTasks(events)) != 1 {
		t.Fatalf("task event count = %d, want 1", len(collectStreamingTasks(events)))
	}
	statuses := collectStreamingStatuses(events)
	if len(statuses) != 3 {
		t.Fatalf("status event count = %d, want 3", len(statuses))
	}
	if statuses[0].Status.State != a2a.TaskStateSubmitted || statuses[1].Status.State != a2a.TaskStateWorking || statuses[2].Status.State != a2a.TaskStateCompleted {
		t.Fatalf("unexpected status sequence: %q, %q, %q", statuses[0].Status.State, statuses[1].Status.State, statuses[2].Status.State)
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
}

func TestRequestHandler_OnSendMessageStream_UsesProvidedContextID(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "reply"}},
			}, nil)
		}
	})

	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))}
	req.Message.ContextID = "ctx-stream"

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
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
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "reply"}},
			}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
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
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(func(*message.ResponseUpdate, error) bool) {}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
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
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
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

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
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
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				ResponseID: "r1",
				Role:       message.RoleAssistant,
				Contents:   message.Contents{&message.TextContent{Text: "reply"}},
			}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
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
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(func(*message.ResponseUpdate, error) bool) {}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
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
	if statuses[0].Status.State != a2a.TaskStateSubmitted || statuses[1].Status.State != a2a.TaskStateCompleted {
		t.Fatalf("unexpected status sequence: %q, %q", statuses[0].Status.State, statuses[1].Status.State)
	}
	if len(collectStreamingArtifacts(events)) != 0 {
		t.Fatalf("artifact event count = %d, want 0", len(collectStreamingArtifacts(events)))
	}
}

func TestRequestHandler_OnCancelTask_ReturnsCanceledTask(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, options ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		allowBackground, _ := agent.GetOption(options, agent.AllowBackgroundResponses)
		if !allowBackground {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, assertErr("expected AllowBackgroundResponses=true"))
			}
		}
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				MessageID:         "m-cancel",
				Role:              message.RoleAssistant,
				Contents:          message.Contents{&message.TextContent{Text: "working"}},
				ContinuationToken: "token-cancel",
			}, nil)
		}
	})
	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a, AllowBackgroundResponses: true})

	task := collectFirstStreamingTask(t, h.SendStreamingMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
	}))
	if task.ID == "" {
		t.Fatal("expected task id")
	}

	canceled, err := h.CancelTask(context.Background(), &a2a.CancelTaskRequest{ID: task.ID})
	if err != nil {
		t.Fatalf("OnCancelTask returned error: %v", err)
	}
	if canceled.Status.State != a2a.TaskStateCanceled {
		t.Fatalf("task status = %q, want %q", canceled.Status.State, a2a.TaskStateCanceled)
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

	var events []a2a.Event
	for evt, err := range stream {
		if err != nil {
			t.Fatalf("stream returned error: %v", err)
		}
		if evt == nil {
			continue
		}
		events = append(events, evt)
	}
	return events
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
