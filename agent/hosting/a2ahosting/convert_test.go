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
