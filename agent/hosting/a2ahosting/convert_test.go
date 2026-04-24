// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/microsoft/agent-framework-go/message"
)

type testTaskInfoProvider struct{}

func (testTaskInfoProvider) TaskInfo() a2a.TaskInfo {
	return a2a.TaskInfo{TaskID: "task-1", ContextID: "ctx-1"}
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

func TestToAgentMessage_WithTextPart_MapsToTextContent(t *testing.T) {
	in := &a2a.Message{ID: "m1", Role: a2a.MessageRoleUser, Parts: a2a.ContentParts{a2a.NewTextPart("hello")}}
	got, err := toAgentMessage(in)
	if err != nil {
		t.Fatalf("toAgentMessage returned error: %v", err)
	}
	if got.ID != "m1" || got.Role != message.RoleUser {
		t.Fatalf("unexpected mapped message: %+v", got)
	}
	if len(got.Contents) != 1 {
		t.Fatalf("contents len = %d, want 1", len(got.Contents))
	}
	text, ok := got.Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want *message.TextContent", got.Contents[0])
	}
	if text.Text != "hello" {
		t.Fatalf("text = %q, want %q", text.Text, "hello")
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

func TestResponseToMessage_WithEmptyAdditionalProperties_PreservesEmptyMetadataMap(t *testing.T) {
	got, err := responseToMessage(testTaskInfoProvider{}, &message.Response{
		AdditionalProperties: map[string]any{},
		Messages:             []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "chunk"}}}},
	})
	if err != nil {
		t.Fatalf("responseToMessage returned error: %v", err)
	}
	if got.Metadata == nil {
		t.Fatal("expected non-nil metadata map")
	}
	if len(got.Metadata) != 0 {
		t.Fatalf("expected empty metadata map, got %#v", got.Metadata)
	}
}

func TestContentsToParts_JSONDataContent_MapsToDataPart(t *testing.T) {
	payload := map[string]any{"ok": true}
	bytes, _ := json.Marshal(payload)
	contents := message.Contents{&message.DataContent{MediaType: "application/json", Data: base64.StdEncoding.EncodeToString(bytes)}}

	parts, err := contentsToParts(contents)
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
	got, err := responseUpdateToMessage(testTaskInfoProvider{}, &message.ResponseUpdate{
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
	got, err := responseUpdateToMessage(testTaskInfoProvider{}, &message.ResponseUpdate{
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
	got, err := responseUpdateToWorkingStatusEvent(testTaskInfoProvider{}, &message.ResponseUpdate{
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
	got, artifactID, err := responseUpdateToArtifactEvent(testTaskInfoProvider{}, "", &message.ResponseUpdate{
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
