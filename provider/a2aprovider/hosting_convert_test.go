// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

type testTaskInfoProvider struct{}

func (testTaskInfoProvider) TaskInfo() a2a.TaskInfo {
	return a2a.TaskInfo{TaskID: "task-1", ContextID: "ctx-1"}
}

func TestBuildTaskUpdateInputs_RejectsInvalidStoredContinuationToken(t *testing.T) {
	_, _, err := buildTaskUpdateInputs(&a2asrv.ExecutorContext{
		StoredTask: &a2a.Task{Metadata: map[string]any{continuationTokenMetadataKey: 42}},
	})
	if err == nil || err.Error() != "stored A2A continuation token is invalid" {
		t.Fatalf("error = %v, want invalid stored continuation token error", err)
	}
}

func TestExecuteTaskUpdate_FailureEmitsFailedAndReturnsError(t *testing.T) {
	wantErr := errors.New("agent failed")
	hostedAgent := agent.New(agent.ProviderConfig{Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, wantErr)
		}
	}}, agent.Config{})
	exec := &executor{agent: hostedAgent}
	execCtx := testExecutorContext()
	execCtx.StoredTask = &a2a.Task{ID: execCtx.TaskID, ContextID: execCtx.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}

	events, err := collectExecutorEvents(func(yield func(a2a.Event, error) bool) error {
		return exec.executeTaskUpdate(context.Background(), execCtx, yield)
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	assertTaskStates(t, events, a2a.TaskStateFailed)
	status := events[0].(*a2a.TaskStatusUpdateEvent)
	if status.Status.Message != nil {
		t.Fatalf("failure status message = %#v, want nil", status.Status.Message)
	}
}

func TestExecuteTaskUpdate_CancellationReturnsWithoutFailureStatus(t *testing.T) {
	hostedAgent := agent.New(agent.ProviderConfig{Run: func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, context.Canceled)
		}
	}}, agent.Config{})
	exec := &executor{agent: hostedAgent}
	execCtx := testExecutorContext()
	execCtx.StoredTask = &a2a.Task{ID: execCtx.TaskID, ContextID: execCtx.ContextID, Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}

	events, err := collectExecutorEvents(func(yield func(a2a.Event, error) bool) error {
		return exec.executeTaskUpdate(context.Background(), execCtx, yield)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func testExecutorContext() *a2asrv.ExecutorContext {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))
	msg.TaskID = "task-1"
	msg.ContextID = "ctx-1"
	return &a2asrv.ExecutorContext{Message: msg, TaskID: "task-1", ContextID: "ctx-1"}
}

func collectExecutorEvents(run func(func(a2a.Event, error) bool) error) ([]a2a.Event, error) {
	var events []a2a.Event
	err := run(func(event a2a.Event, err error) bool {
		if err != nil {
			return false
		}
		events = append(events, event)
		return true
	})
	return events, err
}

func assertTaskStates(t *testing.T, events []a2a.Event, want ...a2a.TaskState) {
	t.Helper()
	var got []a2a.TaskState
	for _, event := range events {
		if status, ok := event.(*a2a.TaskStatusUpdateEvent); ok {
			got = append(got, status.Status.State)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("task states = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("task states = %v, want %v", got, want)
		}
	}
}

func TestToAgentMessage_Nil_ReturnsNil(t *testing.T) {
	got, err := toAgentMessage(nil)
	if err != nil {
		t.Fatalf("toAgentMessage returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil message, got %#v", got)
	}
}

func TestResponseToMessage_NilResponse_ReturnsAgentMessage(t *testing.T) {
	got, err := responseToMessage(testTaskInfoProvider{}, nil)
	if err != nil {
		t.Fatalf("responseToMessage returned error: %v", err)
	}
	if got.Role != a2a.MessageRoleAgent {
		t.Fatalf("role = %q, want %q", got.Role, a2a.MessageRoleAgent)
	}
	if got.ContextID != "ctx-1" || got.TaskID != "task-1" {
		t.Fatalf("unexpected task info in message: task=%q context=%q", got.TaskID, got.ContextID)
	}
}

func TestContentsToParts_JSONDataContent_MapsToDataPart(t *testing.T) {
	payload := map[string]any{"ok": true}
	bytes, _ := json.Marshal(payload)
	contents := message.Contents{&message.DataContent{MediaType: "application/json", Data: base64.StdEncoding.EncodeToString(bytes)}}

	parts, err := contentsToParts(contents, nil)
	if err != nil {
		t.Fatalf("contentsToParts returned error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts len = %d, want 1", len(parts))
	}
	gotData, ok := parts[0].Data().(map[string]any)
	if !ok {
		t.Fatalf("part payload type = %T, want map[string]any", parts[0].Data())
	}
	if okValue, _ := gotData["ok"].(bool); !okValue {
		t.Fatalf("unexpected data part payload: %#v", gotData)
	}
}

func TestResponseToArtifactEvent_NilResponse_ReturnsArtifactEvent(t *testing.T) {
	evt, err := responseToArtifactEvent(testTaskInfoProvider{}, nil)
	if err != nil {
		t.Fatalf("responseToArtifactEvent returned error: %v", err)
	}
	if evt == nil || evt.Artifact == nil {
		t.Fatal("expected non-nil artifact event")
	}
	if evt.TaskID != "task-1" || evt.ContextID != "ctx-1" {
		t.Fatalf("unexpected task info in artifact event: task=%q context=%q", evt.TaskID, evt.ContextID)
	}
}

func TestResponseUpdateToMessage_UsesResponseIDWhenMessageIDMissing(t *testing.T) {
	got, err := responseUpdateToMessage(testTaskInfoProvider{}, &agent.ResponseUpdate{
		ResponseID: "resp-99",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "chunk"}},
	})
	if err != nil {
		t.Fatalf("responseUpdateToMessage returned error: %v", err)
	}
	if got.ID != "resp-99" {
		t.Fatalf("id = %q, want %q", got.ID, "resp-99")
	}
	if got.TaskID != "task-1" || got.ContextID != "ctx-1" {
		t.Fatalf("unexpected task info in message: task=%q context=%q", got.TaskID, got.ContextID)
	}
}

func TestResponseUpdateToMessage_PrefersMessageIDOverResponseID(t *testing.T) {
	got, err := responseUpdateToMessage(testTaskInfoProvider{}, &agent.ResponseUpdate{
		MessageID:  "msg-7",
		ResponseID: "resp-7",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "chunk"}},
	})
	if err != nil {
		t.Fatalf("responseUpdateToMessage returned error: %v", err)
	}
	if got.ID != "msg-7" {
		t.Fatalf("id = %q, want %q", got.ID, "msg-7")
	}
}

func TestResponseUpdateToWorkingStatusEvent_WithContinuationToken_CopiesMetadata(t *testing.T) {
	got, err := responseUpdateToWorkingStatusEvent(testTaskInfoProvider{}, &agent.ResponseUpdate{
		ResponseID:        "resp-1",
		ContinuationToken: "token-123",
		Contents:          message.Contents{&message.TextContent{Text: "working"}},
		AdditionalProperties: map[string]any{
			"count": 2,
		},
	})
	if err != nil {
		t.Fatalf("responseUpdateToWorkingStatusEvent returned error: %v", err)
	}
	if got.Status.State != a2a.TaskStateWorking {
		t.Fatalf("state = %q, want %q", got.Status.State, a2a.TaskStateWorking)
	}
	if got.Metadata[continuationTokenMetadataKey] != "token-123" {
		t.Fatalf("continuation token = %v, want %q", got.Metadata[continuationTokenMetadataKey], "token-123")
	}
	if got.Metadata["count"] != 2 {
		t.Fatalf("metadata count = %v, want %d", got.Metadata["count"], 2)
	}
	if got.Status.Message == nil || got.Status.Message.ID != "resp-1" {
		t.Fatalf("status message = %#v, want response ID %q", got.Status.Message, "resp-1")
	}
}

func TestResponseUpdateToArtifactEvent_UsesResponseIDAndCopiesMetadata(t *testing.T) {
	got, artifactID, err := responseUpdateToArtifactEvent(testTaskInfoProvider{}, "", &agent.ResponseUpdate{
		ResponseID: "resp-42",
		Contents:   message.Contents{&message.TextContent{Text: "chunk"}},
		AdditionalProperties: map[string]any{
			"streamKey": "streamValue",
		},
	})
	if err != nil {
		t.Fatalf("responseUpdateToArtifactEvent returned error: %v", err)
	}
	if artifactID != "resp-42" {
		t.Fatalf("artifact id = %q, want %q", artifactID, "resp-42")
	}
	if got == nil || got.Artifact == nil {
		t.Fatalf("expected non-nil artifact event, got %#v", got)
	}
	if got.Artifact.ID != "resp-42" {
		t.Fatalf("artifact event id = %q, want %q", got.Artifact.ID, "resp-42")
	}
	if got.Metadata["streamKey"] != "streamValue" {
		t.Fatalf("metadata streamKey = %v, want %q", got.Metadata["streamKey"], "streamValue")
	}
	if got.LastChunk {
		t.Fatalf("lastChunk = %v, want false", got.LastChunk)
	}
}

func TestBuildNewMessageInputsNilMessage(t *testing.T) {
	request := &a2a.SendMessageRequest{Message: nil}

	got, err := buildNewMessageInputs(request.Message)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("messages = %#v, want empty", got)
	}
}

func TestBuildNewMessageInputsNilParts(t *testing.T) {
	request := &a2a.SendMessageRequest{Message: &a2a.Message{
		ID:    "test-id",
		Role:  a2a.MessageRoleUser,
		Parts: nil,
	}}

	got, err := buildNewMessageInputs(request.Message)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("messages = %#v, want empty", got)
	}
}

func TestBuildNewMessageInputsText(t *testing.T) {
	request := &a2a.SendMessageRequest{Message: &a2a.Message{
		ID:    "test-id",
		Role:  a2a.MessageRoleUser,
		Parts: a2a.ContentParts{a2a.NewTextPart("Hello, world!")},
	}}

	got, err := buildNewMessageInputs(request.Message)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] == nil {
		t.Fatalf("messages = %#v, want one non-nil message", got)
	}
	msg := got[0]
	if msg.ID != "test-id" {
		t.Errorf("message ID = %q, want test-id", msg.ID)
	}
	if msg.Role != message.RoleUser {
		t.Errorf("role = %q, want user", msg.Role)
	}
	if len(msg.Contents) != 1 {
		t.Fatalf("contents = %#v, want one", msg.Contents)
	}
	text, ok := msg.Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("content = %T, want *message.TextContent", msg.Contents[0])
	}
	if text.Text != "Hello, world!" {
		t.Errorf("text = %q, want Hello, world!", text.Text)
	}
}

func TestMessagesToPartsNilList(t *testing.T) {
	var messages []*message.Message

	got, err := messagesToParts(messages)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("parts = nil, want non-nil")
	}
	if len(got) != 0 {
		t.Fatalf("parts = %#v, want empty", got)
	}
}

func TestMessagesToPartsEmptyList(t *testing.T) {
	messages := []*message.Message{}

	got, err := messagesToParts(messages)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("parts = nil, want non-nil")
	}
	if len(got) != 0 {
		t.Fatalf("parts = %#v, want empty", got)
	}
}

func TestMessagesToPartsTextContent(t *testing.T) {
	messages := []*message.Message{{
		Role:     message.RoleAssistant,
		Contents: message.Contents{&message.TextContent{Text: "Hello from the agent!"}},
	}}

	got, err := messagesToParts(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("parts = %#v, want one", got)
	}
	if got[0].Text() != "Hello from the agent!" {
		t.Errorf("text = %q, want Hello from the agent!", got[0].Text())
	}
}

func TestMessagesToPartsMultipleMessages(t *testing.T) {
	messages := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "First message"}}},
		{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "Second message"}}},
	}

	got, err := messagesToParts(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("parts = %#v, want two", got)
	}
	if got[0].Text() != "First message" {
		t.Errorf("first text = %q, want First message", got[0].Text())
	}
	if got[1].Text() != "Second message" {
		t.Errorf("second text = %q, want Second message", got[1].Text())
	}
}

func TestContentsToPartsEmptyResponseUpdate(t *testing.T) {
	update := &agent.ResponseUpdate{}

	got, err := responseUpdateToParts(update)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("parts = %#v, want empty", got)
	}
}

func TestContentsToPartsTextResponseUpdate(t *testing.T) {
	update := &agent.ResponseUpdate{
		Role:     message.RoleAssistant,
		Contents: message.Contents{&message.TextContent{Text: "Hello from streaming!"}},
	}

	got, err := responseUpdateToParts(update)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("parts = %#v, want one", got)
	}
	if got[0].Text() != "Hello from streaming!" {
		t.Errorf("text = %q, want Hello from streaming!", got[0].Text())
	}
}

func TestContentsToPartsMultipleResponseUpdate(t *testing.T) {
	update := &agent.ResponseUpdate{
		Role: message.RoleAssistant,
		Contents: message.Contents{
			&message.TextContent{Text: "First chunk"},
			&message.TextContent{Text: "Second chunk"},
		},
	}

	got, err := responseUpdateToParts(update)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("parts = %#v, want two", got)
	}
	if got[0].Text() != "First chunk" {
		t.Errorf("first text = %q, want First chunk", got[0].Text())
	}
	if got[1].Text() != "Second chunk" {
		t.Errorf("second text = %q, want Second chunk", got[1].Text())
	}
}

func TestContentsToPartsUnsupportedResponseUpdate(t *testing.T) {
	update := &agent.ResponseUpdate{
		Role: message.RoleAssistant,
		Contents: message.Contents{
			&message.TextContent{Text: "Supported text"},
			&message.FunctionCallContent{CallID: "call-1", Name: "myFunction"},
		},
	}

	got, err := responseUpdateToParts(update)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("parts = %#v, want only the supported text part", got)
	}
	if got[0].Text() != "Supported text" {
		t.Errorf("text = %q, want Supported text", got[0].Text())
	}
}
