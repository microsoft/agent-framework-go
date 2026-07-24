// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func TestResponse_Update_NilUpdate(t *testing.T) {
	resp := &agent.Response{}
	resp.Update(nil)
	if len(resp.Messages) != 0 {
		t.Errorf("expected no messages, got %d", len(resp.Messages))
	}
}

func TestResponseUpdate_Usage_NilUpdate(t *testing.T) {
	var update *agent.ResponseUpdate
	if got := update.Usage(); !reflect.DeepEqual(got, message.UsageDetails{}) {
		t.Fatalf("expected zero usage for nil update, got %+v", got)
	}
}

func TestResponse_Update_EmptyResponse(t *testing.T) {
	resp := &agent.Response{}
	update := &agent.ResponseUpdate{
		AgentID:    "author1",
		MessageID:  "msg1",
		AuthorName: "Test Author",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "Hello"}},
	}
	resp.Update(update)

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	msg := resp.Messages[0]
	if resp.AgentID != "author1" {
		t.Errorf("expected AgentID 'author1', got %q", resp.AgentID)
	}
	if msg.ID != "msg1" {
		t.Errorf("expected ID 'msg1', got %q", msg.ID)
	}
	if msg.AuthorName != "Test Author" {
		t.Errorf("expected AuthorName 'Test Author', got %q", msg.AuthorName)
	}
	if msg.Role != message.RoleAssistant {
		t.Errorf("expected Role 'assistant', got %q", msg.Role)
	}
	if len(msg.Contents) != 1 {
		t.Errorf("expected 1 content, got %d", len(msg.Contents))
	}
}

func TestResponse_Update_SameMessage(t *testing.T) {
	resp := &agent.Response{}

	// First update
	update1 := &agent.ResponseUpdate{
		AgentID:    "author1",
		MessageID:  "msg1",
		AuthorName: "Test Author",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "Hello"}},
	}
	resp.Update(update1)

	// Second update with same identifiers (empty identifiers means same message)
	update2 := &agent.ResponseUpdate{
		Contents: message.Contents{&message.TextContent{Text: " World"}},
	}
	resp.Update(update2)

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resp.Messages))
	}
	msg := resp.Messages[0]
	if len(msg.Contents) != 2 {
		t.Errorf("expected 2 contents, got %d", len(msg.Contents))
	}
	text := resp.String()
	if text != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", text)
	}
}

func TestResponse_Update_DifferentMessageID(t *testing.T) {
	resp := &agent.Response{}

	// First update
	update1 := &agent.ResponseUpdate{
		MessageID: "msg1",
		Contents:  message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with different message ID
	update2 := &agent.ResponseUpdate{
		MessageID: "msg2",
		Contents:  message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}
	if resp.Messages[0].ID != "msg1" {
		t.Errorf("expected first message ID 'msg1', got %q", resp.Messages[0].ID)
	}
	if resp.Messages[1].ID != "msg2" {
		t.Errorf("expected second message ID 'msg2', got %q", resp.Messages[1].ID)
	}
}

func TestResponse_Update_DifferentAuthorName(t *testing.T) {
	resp := &agent.Response{}

	// First update
	update1 := &agent.ResponseUpdate{
		AuthorName: "Author1",
		Contents:   message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with different author name
	update2 := &agent.ResponseUpdate{
		AuthorName: "Author2",
		Contents:   message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}
	if resp.Messages[0].AuthorName != "Author1" {
		t.Errorf("expected first author 'Author1', got %q", resp.Messages[0].AuthorName)
	}
	if resp.Messages[1].AuthorName != "Author2" {
		t.Errorf("expected second author 'Author2', got %q", resp.Messages[1].AuthorName)
	}
}

func TestResponse_Update_DifferentRole(t *testing.T) {
	resp := &agent.Response{}

	// First update
	update1 := &agent.ResponseUpdate{
		Role:     message.RoleAssistant,
		Contents: message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with different role
	update2 := &agent.ResponseUpdate{
		Role:     message.RoleUser,
		Contents: message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}
	if resp.Messages[0].Role != message.RoleAssistant {
		t.Errorf("expected first role 'assistant', got %q", resp.Messages[0].Role)
	}
	if resp.Messages[1].Role != message.RoleUser {
		t.Errorf("expected second role 'user', got %q", resp.Messages[1].Role)
	}
}

func TestResponse_Update_CreatedAt(t *testing.T) {
	resp := &agent.Response{}

	time1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	time2 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	time3 := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)

	// First update with time1
	update1 := &agent.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time1,
		Contents:  message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if !resp.Messages[0].CreatedAt.Equal(time1) {
		t.Errorf("expected CreatedAt %v, got %v", time1, resp.Messages[0].CreatedAt)
	}

	// Second update with later time - should update
	update2 := &agent.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time2,
		Contents:  message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if !resp.Messages[0].CreatedAt.Equal(time2) {
		t.Errorf("expected CreatedAt %v, got %v", time2, resp.Messages[0].CreatedAt)
	}

	// Third update with even later time - should update
	update3 := &agent.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time3,
		Contents:  message.Contents{&message.TextContent{Text: "Third"}},
	}
	resp.Update(update3)

	if !resp.Messages[0].CreatedAt.Equal(time3) {
		t.Errorf("expected CreatedAt %v, got %v", time3, resp.Messages[0].CreatedAt)
	}
}

func TestResponse_Update_CreatedAt_EarlierIgnored(t *testing.T) {
	resp := &agent.Response{}

	time1 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	time2 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // Earlier time

	update1 := &agent.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time1,
		Contents:  message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with earlier time - should NOT update
	update2 := &agent.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time2,
		Contents:  message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if !resp.Messages[0].CreatedAt.Equal(time1) {
		t.Errorf("expected CreatedAt to remain %v, got %v", time1, resp.Messages[0].CreatedAt)
	}
}

func TestResponse_Update_ContinuationToken(t *testing.T) {
	resp := &agent.Response{}

	token1 := "token1"
	token2 := "token2"

	// First update with token
	update1 := &agent.ResponseUpdate{
		MessageID:         "msg1",
		ContinuationToken: token1,
		Contents:          message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if resp.ContinuationToken != token1 {
		t.Errorf("expected ContinuationToken %v, got %v", token1, resp.ContinuationToken)
	}

	// Second update with different token
	update2 := &agent.ResponseUpdate{
		MessageID:         "msg1",
		ContinuationToken: token2,
		Contents:          message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if resp.ContinuationToken != token2 {
		t.Errorf("expected ContinuationToken %v, got %v", token2, resp.ContinuationToken)
	}

	// Third update with empty token - should clear
	update3 := &agent.ResponseUpdate{
		MessageID:         "msg1",
		ContinuationToken: "",
		Contents:          message.Contents{&message.TextContent{Text: "Third"}},
	}
	resp.Update(update3)

	if resp.ContinuationToken != "" {
		t.Errorf("expected ContinuationToken empty, got %v", resp.ContinuationToken)
	}
}

func TestResponse_Update_FinishReason(t *testing.T) {
	resp := &agent.Response{}

	resp.Update(&agent.ResponseUpdate{
		MessageID:    "msg1",
		FinishReason: "tool_calls",
		Contents:     message.Contents{&message.TextContent{Text: "Calling"}},
	})
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("expected FinishReason tool_calls, got %q", resp.FinishReason)
	}

	resp.Update(&agent.ResponseUpdate{
		MessageID: "msg1",
		Contents:  message.Contents{&message.TextContent{Text: " tools"}},
	})
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("expected empty update to preserve FinishReason, got %q", resp.FinishReason)
	}

	resp.Update(&agent.ResponseUpdate{
		MessageID:    "msg1",
		FinishReason: "stop",
		Contents:     message.Contents{&message.TextContent{Text: " done"}},
	})
	if resp.FinishReason != "stop" {
		t.Fatalf("expected latest FinishReason stop, got %q", resp.FinishReason)
	}
}

func TestResponse_CreatedAt(t *testing.T) {
	resp := &agent.Response{}

	time1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	time2 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	time3 := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC) // Earlier time

	// First update with time1
	update1 := &agent.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time1,
		Contents:  message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if !resp.CreatedAt.Equal(time1) {
		t.Errorf("expected resp.CreatedAt %v, got %v", time1, resp.CreatedAt)
	}

	// Second update with later time - should update
	update2 := &agent.ResponseUpdate{
		MessageID: "msg2",
		CreatedAt: time2,
		Contents:  message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if !resp.CreatedAt.Equal(time2) {
		t.Errorf("expected resp.CreatedAt %v, got %v", time2, resp.CreatedAt)
	}

	// Third update with earlier time - should NOT update
	update3 := &agent.ResponseUpdate{
		MessageID: "msg3",
		CreatedAt: time3,
		Contents:  message.Contents{&message.TextContent{Text: "Third"}},
	}
	resp.Update(update3)

	if !resp.CreatedAt.Equal(time2) {
		t.Errorf("expected resp.CreatedAt to remain %v, got %v", time2, resp.CreatedAt)
	}
}

func TestResponse_Update_AdditionalProperties(t *testing.T) {
	resp := &agent.Response{}

	// First update with properties
	update1 := &agent.ResponseUpdate{
		MessageID: "msg1",
		AdditionalProperties: map[string]any{
			"key1": "value1",
			"key2": 123,
		},
		Contents: message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if len(resp.Messages[0].AdditionalProperties) != 2 {
		t.Errorf("expected 2 additional properties, got %d", len(resp.Messages[0].AdditionalProperties))
	}
	if resp.Messages[0].AdditionalProperties["key1"] != "value1" {
		t.Errorf("expected key1 'value1', got %v", resp.Messages[0].AdditionalProperties["key1"])
	}

	// Second update with more properties
	update2 := &agent.ResponseUpdate{
		MessageID: "msg1",
		AdditionalProperties: map[string]any{
			"key2": 456, // Override
			"key3": "value3",
		},
		Contents: message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if len(resp.Messages[0].AdditionalProperties) != 3 {
		t.Errorf("expected 3 additional properties, got %d", len(resp.Messages[0].AdditionalProperties))
	}
	if resp.Messages[0].AdditionalProperties["key2"] != 456 {
		t.Errorf("expected key2 456, got %v", resp.Messages[0].AdditionalProperties["key2"])
	}
	if resp.Messages[0].AdditionalProperties["key3"] != "value3" {
		t.Errorf("expected key3 'value3', got %v", resp.Messages[0].AdditionalProperties["key3"])
	}
}

func TestResponse_Update_RawRepresentation(t *testing.T) {
	resp := &agent.Response{}

	// First update
	update1 := &agent.ResponseUpdate{
		MessageID:         "msg1",
		RawRepresentation: "raw1",
		Contents:          message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if resp.Messages[0].RawRepresentation != "raw1" {
		t.Errorf("expected RawRepresentation 'raw1', got %v", resp.Messages[0].RawRepresentation)
	}

	// Second update - should create slice
	update2 := &agent.ResponseUpdate{
		MessageID:         "msg1",
		RawRepresentation: "raw2",
		Contents:          message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	rawSlice, ok := resp.Messages[0].RawRepresentation.([]any)
	if !ok {
		t.Fatalf("expected RawRepresentation to be []any, got %T", resp.Messages[0].RawRepresentation)
	}
	if len(rawSlice) != 2 {
		t.Errorf("expected 2 raw representations, got %d", len(rawSlice))
	}
	if rawSlice[0] != "raw1" {
		t.Errorf("expected first raw 'raw1', got %v", rawSlice[0])
	}
	if rawSlice[1] != "raw2" {
		t.Errorf("expected second raw 'raw2', got %v", rawSlice[1])
	}

	// Third update - should append to slice
	update3 := &agent.ResponseUpdate{
		MessageID:         "msg1",
		RawRepresentation: "raw3",
		Contents:          message.Contents{&message.TextContent{Text: "Third"}},
	}
	resp.Update(update3)

	rawSlice, ok = resp.Messages[0].RawRepresentation.([]any)
	if !ok {
		t.Fatalf("expected RawRepresentation to be []any, got %T", resp.Messages[0].RawRepresentation)
	}
	if len(rawSlice) != 3 {
		t.Errorf("expected 3 raw representations, got %d", len(rawSlice))
	}
	if rawSlice[2] != "raw3" {
		t.Errorf("expected third raw 'raw3', got %v", rawSlice[2])
	}
}

func TestResponse_ToUpdates_RoundTripPreservesRawRepresentationWithContinuationToken(t *testing.T) {
	// A response with a message raw representation and a continuation token emits
	// a trailing metadata-only update (RawRepresentation nil). Collecting the
	// updates back must not fold that nil into the message's raw data.
	original := &agent.Response{
		ContinuationToken: "token-123",
		Messages: []*message.Message{
			{
				ID:                "msg1",
				Role:              message.RoleAssistant,
				RawRepresentation: "raw1",
				Contents:          message.Contents{&message.TextContent{Text: "Hello"}},
			},
		},
	}

	var collected agent.Response
	for _, update := range original.ToUpdates() {
		collected.Update(update)
	}

	if got := collected.Messages[0].RawRepresentation; got != "raw1" {
		t.Errorf("expected RawRepresentation to round-trip as 'raw1', got %v", got)
	}
	if collected.ContinuationToken != "token-123" {
		t.Errorf("expected ContinuationToken 'token-123', got %q", collected.ContinuationToken)
	}
}

func TestResponse_Coalesce_PreservesEmptyMessagesAndWhitespaceText(t *testing.T) {
	resp := &agent.Response{}
	resp.Update(&agent.ResponseUpdate{
		MessageID:         "metadata-only",
		RawRepresentation: "raw",
	})
	resp.Update(&agent.ResponseUpdate{
		MessageID: "whitespace",
		Contents:  message.Contents{&message.TextContent{Text: "  \t\n"}},
	})
	resp.Update(&agent.ResponseUpdate{
		MessageID: "content",
		Contents:  message.Contents{&message.TextContent{Text: "kept"}},
	})

	resp.Coalesce()

	if len(resp.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(resp.Messages))
	}
	if got := resp.Messages[0].ID; got != "metadata-only" {
		t.Fatalf("message 0 ID = %q, want metadata-only", got)
	}
	if len(resp.Messages[0].Contents) != 0 {
		t.Fatalf("message 0 contents count = %d, want 0", len(resp.Messages[0].Contents))
	}
	if got := resp.Messages[1].ID; got != "whitespace" {
		t.Fatalf("message 1 ID = %q, want whitespace", got)
	}
	if got := resp.Messages[1].Contents.Text(); got != "  \t\n" {
		t.Fatalf("message 1 text = %q, want whitespace", got)
	}
	if got := resp.Messages[2].ID; got != "content" {
		t.Fatalf("message 2 ID = %q, want content", got)
	}
	if got := resp.Messages[2].Contents.Text(); got != "kept" {
		t.Fatalf("message 2 text = %q, want kept", got)
	}
}

func TestResponse_Update_PreferLatestValues(t *testing.T) {
	resp := &agent.Response{}

	// First update with partial data
	update1 := &agent.ResponseUpdate{
		MessageID:  "msg1",
		AgentID:    "author1",
		AuthorName: "Author One",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update should not override non-empty values with empty ones
	update2 := &agent.ResponseUpdate{
		MessageID: "msg1",
		// Omitting AgentID, AuthorName, Role - should keep previous values
		Contents: message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	msg := resp.Messages[0]
	if resp.AgentID != "author1" {
		t.Errorf("expected AgentID 'author1', got %q", resp.AgentID)
	}
	if msg.AuthorName != "Author One" {
		t.Errorf("expected AuthorName 'Author One', got %q", msg.AuthorName)
	}
	if msg.Role != message.RoleAssistant {
		t.Errorf("expected Role 'assistant', got %q", msg.Role)
	}
}

func TestResponse_Update_OverrideWithNewValues(t *testing.T) {
	resp := &agent.Response{}

	// First update
	update1 := &agent.ResponseUpdate{
		MessageID:  "msg1",
		AgentID:    "author1",
		AuthorName: "Author One",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with different message ID - creates new message
	update2 := &agent.ResponseUpdate{
		MessageID:  "msg2",
		AgentID:    "author2",
		AuthorName: "Author Two",
		Role:       message.RoleUser,
		Contents:   message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}

	msg := resp.Messages[1]
	if resp.AgentID != "author2" {
		t.Errorf("expected AgentID 'author2', got %q", resp.AgentID)
	}
	if msg.AuthorName != "Author Two" {
		t.Errorf("expected AuthorName 'Author Two', got %q", msg.AuthorName)
	}
	if msg.Role != message.RoleUser {
		t.Errorf("expected Role 'user', got %q", msg.Role)
	}
}

func TestResponse_Update_MultipleMessages(t *testing.T) {
	resp := &agent.Response{}

	// Create first message
	update1 := &agent.ResponseUpdate{
		MessageID: "msg1",
		Contents:  message.Contents{&message.TextContent{Text: "Message 1"}},
	}
	resp.Update(update1)

	// Create second message
	update2 := &agent.ResponseUpdate{
		MessageID: "msg2",
		Contents:  message.Contents{&message.TextContent{Text: "Message 2"}},
	}
	resp.Update(update2)

	// Create third message
	update3 := &agent.ResponseUpdate{
		MessageID: "msg3",
		Contents:  message.Contents{&message.TextContent{Text: "Message 3"}},
	}
	resp.Update(update3)

	if len(resp.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(resp.Messages))
	}

	for i, expectedText := range []string{"Message 1", "Message 2", "Message 3"} {
		text := resp.Messages[i].String()
		if text != expectedText {
			t.Errorf("expected message %d to be %q, got %q", i, expectedText, text)
		}
	}
}

func TestResponse_ToUpdates_ProducesUpdates(t *testing.T) {
	createdAt := time.Date(2024, 11, 10, 9, 20, 0, 0, time.UTC)
	resp := &agent.Response{
		AgentID:              "agentId",
		ID:                   "12345",
		FinishReason:         "stop",
		CreatedAt:            createdAt,
		AdditionalProperties: map[string]any{"key1": "value1", "key2": 42},
		Messages: []*message.Message{
			{
				Role: message.Role("customRole"),
				ID:   "someMessage",
				Contents: message.Contents{
					&message.TextContent{Text: "Text"},
					&message.UsageContent{Details: message.UsageDetails{TotalTokenCount: 100}},
				},
			},
		},
	}

	updates := resp.ToUpdates()
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}

	update0 := updates[0]
	if update0.AgentID != "agentId" {
		t.Errorf("expected AgentID agentId, got %q", update0.AgentID)
	}
	if update0.ResponseID != "12345" {
		t.Errorf("expected ResponseID 12345, got %q", update0.ResponseID)
	}
	if update0.FinishReason != "stop" {
		t.Errorf("expected FinishReason stop, got %q", update0.FinishReason)
	}
	if update0.MessageID != "someMessage" {
		t.Errorf("expected MessageID someMessage, got %q", update0.MessageID)
	}
	if !update0.CreatedAt.Equal(createdAt) {
		t.Errorf("expected CreatedAt %v, got %v", createdAt, update0.CreatedAt)
	}
	if update0.Role != message.Role("customRole") {
		t.Errorf("expected customRole, got %q", update0.Role)
	}
	if update0.String() != "Text" {
		t.Errorf("expected Text, got %q", update0.String())
	}

	update1 := updates[1]
	if update1.AdditionalProperties["key1"] != "value1" {
		t.Errorf("expected key1 value1, got %v", update1.AdditionalProperties["key1"])
	}
	if update1.FinishReason != "" {
		t.Errorf("expected empty FinishReason, got %q", update1.FinishReason)
	}
	if update1.AdditionalProperties["key2"] != 42 {
		t.Errorf("expected key2 42, got %v", update1.AdditionalProperties["key2"])
	}
	if len(update1.Contents) != 1 {
		t.Fatalf("expected 1 extra content, got %d", len(update1.Contents))
	}
	usageContent, ok := update1.Contents[0].(*message.UsageContent)
	if !ok {
		t.Fatalf("expected UsageContent, got %T", update1.Contents[0])
	}
	if usageContent.Details.TotalTokenCount != 100 {
		t.Errorf("expected total token count 100, got %d", usageContent.Details.TotalTokenCount)
	}
}

func TestResponse_ToUpdates_WithNoMessagesProducesEmptySlice(t *testing.T) {
	resp := &agent.Response{}

	updates := resp.ToUpdates()

	if len(updates) != 0 {
		t.Fatalf("expected no updates, got %d", len(updates))
	}
}

func TestResponse_ToUpdates_WithFinishReasonOnlyProducesEmptySlice(t *testing.T) {
	resp := &agent.Response{FinishReason: "length"}

	updates := resp.ToUpdates()

	if len(updates) != 0 {
		t.Fatalf("expected no updates, got %d", len(updates))
	}
}

func TestResponse_ToUpdates_WithUsageOnlyProducesSingleUpdate(t *testing.T) {
	resp := &agent.Response{
		Messages: []*message.Message{
			{
				Contents: message.Contents{
					&message.UsageContent{Details: message.UsageDetails{TotalTokenCount: 100}},
				},
			},
		},
	}

	updates := resp.ToUpdates()

	if len(updates) != 2 {
		t.Fatalf("expected message update and usage update, got %d", len(updates))
	}
	usageContent, ok := updates[1].Contents[0].(*message.UsageContent)
	if !ok {
		t.Fatalf("expected UsageContent, got %T", updates[1].Contents[0])
	}
	if usageContent.Details.TotalTokenCount != 100 {
		t.Errorf("expected total token count 100, got %d", usageContent.Details.TotalTokenCount)
	}
}

func TestResponse_ToUpdates_PropagatesMessageCreatedAtOverResponseCreatedAt(t *testing.T) {
	responseCreatedAt := time.Date(2024, 11, 10, 9, 20, 0, 0, time.UTC)
	messageCreatedAt := time.Date(2024, 11, 11, 9, 20, 0, 0, time.UTC)
	resp := &agent.Response{
		AgentID:   "agentId",
		ID:        "12345",
		CreatedAt: responseCreatedAt,
		Messages: []*message.Message{
			{
				Role:      message.Role("customRole"),
				ID:        "someMessage",
				CreatedAt: messageCreatedAt,
				Contents:  message.Contents{&message.TextContent{Text: "Text"}},
			},
		},
	}

	updates := resp.ToUpdates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if !updates[0].CreatedAt.Equal(messageCreatedAt) {
		t.Errorf("expected per-message CreatedAt %v, got %v", messageCreatedAt, updates[0].CreatedAt)
	}
}

func TestResponse_ToUpdates_FallsBackToResponseCreatedAtWhenMessageCreatedAtIsZero(t *testing.T) {
	responseCreatedAt := time.Date(2024, 11, 10, 9, 20, 0, 0, time.UTC)
	resp := &agent.Response{
		AgentID:   "agentId",
		ID:        "12345",
		CreatedAt: responseCreatedAt,
		Messages: []*message.Message{
			{
				Role:     message.Role("customRole"),
				ID:       "someMessage",
				Contents: message.Contents{&message.TextContent{Text: "Text"}},
			},
		},
	}

	updates := resp.ToUpdates()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if !updates[0].CreatedAt.Equal(responseCreatedAt) {
		t.Errorf("expected response-level CreatedAt %v, got %v", responseCreatedAt, updates[0].CreatedAt)
	}
}

func TestResponse_ToUpdates_WithAdditionalPropertiesOnlyProducesSingleUpdate(t *testing.T) {
	resp := &agent.Response{
		AdditionalProperties: map[string]any{"key": "value"},
	}

	updates := resp.ToUpdates()

	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].AdditionalProperties["key"] != "value" {
		t.Errorf("expected key value, got %v", updates[0].AdditionalProperties["key"])
	}
}

func TestResponse_ToUpdates_PropagatesContinuationToken(t *testing.T) {
	resp := &agent.Response{
		ContinuationToken: "tok-123",
		Messages: []*message.Message{
			{
				Role:     message.RoleAssistant,
				Contents: message.Contents{&message.TextContent{Text: "Text"}},
			},
		},
	}

	updates := resp.ToUpdates()

	// The token must survive a ToUpdates/Collect round-trip.
	var roundTripped agent.Response
	for _, update := range updates {
		roundTripped.Update(update)
	}
	roundTripped.Coalesce()

	if roundTripped.ContinuationToken != "tok-123" {
		t.Errorf("expected ContinuationToken tok-123, got %q", roundTripped.ContinuationToken)
	}
}
