// Copyright (c) Microsoft. All rights reserved.

package a2ahosting_test

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/hosting/a2ahosting"
	"github.com/microsoft/agent-framework-go/message"
)

func newTestAgent(runFn func(context.Context, []*message.Message, ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error]) *agent.Agent {
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
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				MessageID: "m1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "hello from agent"}},
			}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
	result, err := h.OnSendMessage(context.Background(), &a2a.MessageSendParams{
		Message: &a2a.Message{Role: a2a.MessageRoleUser, Parts: []a2a.Part{a2a.TextPart{Text: "ping"}}},
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
	textPart, ok := msg.Parts[0].(a2a.TextPart)
	if !ok {
		t.Fatalf("part type = %T, want a2a.TextPart", msg.Parts[0])
	}
	if textPart.Text != "hello from agent" {
		t.Fatalf("text = %q, want %q", textPart.Text, "hello from agent")
	}
}

func TestRequestHandler_OnSendMessage_WithReferenceTaskIDs_ReturnsError(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{Contents: message.Contents{&message.TextContent{Text: "ignored"}}}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
	_, err := h.OnSendMessage(context.Background(), &a2a.MessageSendParams{
		Message: &a2a.Message{
			Role:           a2a.MessageRoleUser,
			Parts:          []a2a.Part{a2a.TextPart{Text: "ping"}},
			ReferenceTasks: []a2a.TaskID{"task-123"},
		},
	})
	if err == nil {
		t.Fatal("expected error for referenceTaskIds")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "referencetaskids") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequestHandler_OnSendMessage_PreservesContextID(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				MessageID: "m-context",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "done"}},
			}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a})
	result, err := h.OnSendMessage(context.Background(), &a2a.MessageSendParams{
		Message: &a2a.Message{ContextID: "ctx-123", Role: a2a.MessageRoleUser, Parts: []a2a.Part{a2a.TextPart{Text: "ping"}}},
	})
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

func TestRequestHandler_OnSendMessageStream_UsesTaskLifecycle_WhenContinuationTokenPresent(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		allowBackground, _ := agentopt.Get(options, agentopt.AllowBackgroundResponses)
		if !allowBackground {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, assertErr("expected AllowBackgroundResponses=true"))
			}
		}
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				MessageID:         "m2",
				Role:              message.RoleAssistant,
				Contents:          message.Contents{&message.TextContent{Text: "working"}},
				ContinuationToken: "token-123",
			}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a, AllowBackgroundResponses: true})
	stream := h.OnSendMessageStream(context.Background(), &a2a.MessageSendParams{
		Message: &a2a.Message{Role: a2a.MessageRoleUser, Parts: []a2a.Part{a2a.TextPart{Text: "ping"}}},
	})

	var submitted, working bool
	for evt, err := range stream {
		if err != nil {
			t.Fatalf("stream returned error: %v", err)
		}
		status, ok := evt.(*a2a.TaskStatusUpdateEvent)
		if !ok {
			continue
		}
		switch status.Status.State {
		case a2a.TaskStateSubmitted:
			submitted = true
		case a2a.TaskStateWorking:
			working = true
			if got := status.Metadata["__a2a__continuationToken"]; got != "token-123" {
				t.Fatalf("continuation token metadata = %v, want %q", got, "token-123")
			}
			break
		}
		if working {
			break
		}
	}

	if !submitted {
		t.Fatal("expected submitted status update")
	}
	if !working {
		t.Fatal("expected working status update")
	}
}

func TestRequestHandler_OnSendMessageStream_WhenContinuationTokenAndNoMessages_StatusMessageIsNil(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{ContinuationToken: "token-no-msg"}, nil)
		}
	})

	h := a2ahosting.NewRequestHandler(a2ahosting.ExecutorConfig{Agent: a, AllowBackgroundResponses: true})
	stream := h.OnSendMessageStream(context.Background(), &a2a.MessageSendParams{
		Message: &a2a.Message{Role: a2a.MessageRoleUser, Parts: []a2a.Part{a2a.TextPart{Text: "ping"}}},
	})

	var working *a2a.TaskStatusUpdateEvent
	for evt, err := range stream {
		if err != nil {
			t.Fatalf("stream returned error: %v", err)
		}
		status, ok := evt.(*a2a.TaskStatusUpdateEvent)
		if !ok || status.Status.State != a2a.TaskStateWorking {
			continue
		}
		working = status
		break
	}
	if working == nil {
		t.Fatal("expected working status event")
	}
	if working.Status.Message != nil && len(working.Status.Message.Parts) > 0 {
		t.Fatalf("expected nil or empty working status message, got %#v", working.Status.Message)
	}
}

func TestRequestHandler_OnCancelTask_ReturnsCanceledTask(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
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

	stream := h.OnSendMessageStream(context.Background(), &a2a.MessageSendParams{
		Message: &a2a.Message{
			Role:  a2a.MessageRoleUser,
			Parts: []a2a.Part{a2a.TextPart{Text: "hi"}},
		},
	})

	var taskID a2a.TaskID
	for evt, err := range stream {
		if err != nil {
			t.Fatalf("OnSendMessageStream returned error: %v", err)
		}
		status, ok := evt.(*a2a.TaskStatusUpdateEvent)
		if !ok {
			continue
		}
		taskID = status.TaskID
		break
	}
	if taskID == "" {
		t.Fatal("expected task id")
	}

	canceled, err := h.OnCancelTask(context.Background(), &a2a.TaskIDParams{ID: taskID})
	if err != nil {
		t.Fatalf("OnCancelTask returned error: %v", err)
	}
	if canceled.Status.State != a2a.TaskStateCanceled {
		t.Fatalf("task status = %q, want %q", canceled.Status.State, a2a.TaskStateCanceled)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
