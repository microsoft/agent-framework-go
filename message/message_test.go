// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/message"
)

func TestResponse_Update_NilUpdate(t *testing.T) {
	resp := &message.Response{}
	resp.Update(nil)
	if len(resp.Messages) != 0 {
		t.Errorf("expected no messages, got %d", len(resp.Messages))
	}
}

func TestResponse_Update_EmptyResponse(t *testing.T) {
	resp := &message.Response{}
	update := &message.ResponseUpdate{
		AuthorID:   "author1",
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
	if msg.AuthorID != "author1" {
		t.Errorf("expected AuthorID 'author1', got %q", msg.AuthorID)
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
	resp := &message.Response{}

	// First update
	update1 := &message.ResponseUpdate{
		AuthorID:   "author1",
		MessageID:  "msg1",
		AuthorName: "Test Author",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "Hello"}},
	}
	resp.Update(update1)

	// Second update with same identifiers (empty identifiers means same message)
	update2 := &message.ResponseUpdate{
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
	resp := &message.Response{}

	// First update
	update1 := &message.ResponseUpdate{
		MessageID: "msg1",
		Contents:  message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with different message ID
	update2 := &message.ResponseUpdate{
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
	resp := &message.Response{}

	// First update
	update1 := &message.ResponseUpdate{
		AuthorName: "Author1",
		Contents:   message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with different author name
	update2 := &message.ResponseUpdate{
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
	resp := &message.Response{}

	// First update
	update1 := &message.ResponseUpdate{
		Role:     message.RoleAssistant,
		Contents: message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with different role
	update2 := &message.ResponseUpdate{
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
	resp := &message.Response{}

	time1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	time2 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	time3 := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)

	// First update with time1
	update1 := &message.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time1,
		Contents:  message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if !resp.Messages[0].CreatedAt.Equal(time1) {
		t.Errorf("expected CreatedAt %v, got %v", time1, resp.Messages[0].CreatedAt)
	}

	// Second update with later time - should update
	update2 := &message.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time2,
		Contents:  message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if !resp.Messages[0].CreatedAt.Equal(time2) {
		t.Errorf("expected CreatedAt %v, got %v", time2, resp.Messages[0].CreatedAt)
	}

	// Third update with even later time - should update
	update3 := &message.ResponseUpdate{
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
	resp := &message.Response{}

	time1 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	time2 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // Earlier time

	update1 := &message.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time1,
		Contents:  message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with earlier time - should NOT update
	update2 := &message.ResponseUpdate{
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
	resp := &message.Response{}

	token1 := "token1"
	token2 := "token2"

	// First update with token
	update1 := &message.ResponseUpdate{
		MessageID:         "msg1",
		ContinuationToken: token1,
		Contents:          message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if resp.ContinuationToken != token1 {
		t.Errorf("expected ContinuationToken %v, got %v", token1, resp.ContinuationToken)
	}

	// Second update with different token
	update2 := &message.ResponseUpdate{
		MessageID:         "msg1",
		ContinuationToken: token2,
		Contents:          message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if resp.ContinuationToken != token2 {
		t.Errorf("expected ContinuationToken %v, got %v", token2, resp.ContinuationToken)
	}

	// Third update with empty token - should clear
	update3 := &message.ResponseUpdate{
		MessageID:         "msg1",
		ContinuationToken: "",
		Contents:          message.Contents{&message.TextContent{Text: "Third"}},
	}
	resp.Update(update3)

	if resp.ContinuationToken != "" {
		t.Errorf("expected ContinuationToken empty, got %v", resp.ContinuationToken)
	}
}

func TestResponse_CreatedAt(t *testing.T) {
	resp := &message.Response{}

	time1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	time2 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	time3 := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC) // Earlier time

	// First update with time1
	update1 := &message.ResponseUpdate{
		MessageID: "msg1",
		CreatedAt: time1,
		Contents:  message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if !resp.CreatedAt.Equal(time1) {
		t.Errorf("expected resp.CreatedAt %v, got %v", time1, resp.CreatedAt)
	}

	// Second update with later time - should update
	update2 := &message.ResponseUpdate{
		MessageID: "msg2",
		CreatedAt: time2,
		Contents:  message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if !resp.CreatedAt.Equal(time2) {
		t.Errorf("expected resp.CreatedAt %v, got %v", time2, resp.CreatedAt)
	}

	// Third update with earlier time - should NOT update
	update3 := &message.ResponseUpdate{
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
	resp := &message.Response{}

	// First update with properties
	update1 := &message.ResponseUpdate{
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
	update2 := &message.ResponseUpdate{
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
	resp := &message.Response{}

	// First update
	update1 := &message.ResponseUpdate{
		MessageID:         "msg1",
		RawRepresentation: "raw1",
		Contents:          message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	if resp.Messages[0].RawRepresentation != "raw1" {
		t.Errorf("expected RawRepresentation 'raw1', got %v", resp.Messages[0].RawRepresentation)
	}

	// Second update - should create slice
	update2 := &message.ResponseUpdate{
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
	update3 := &message.ResponseUpdate{
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

func TestResponse_Update_PreferLatestValues(t *testing.T) {
	resp := &message.Response{}

	// First update with partial data
	update1 := &message.ResponseUpdate{
		MessageID:  "msg1",
		AuthorID:   "author1",
		AuthorName: "Author One",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update should not override non-empty values with empty ones
	update2 := &message.ResponseUpdate{
		MessageID: "msg1",
		// Omitting AuthorID, AuthorName, Role - should keep previous values
		Contents: message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	msg := resp.Messages[0]
	if msg.AuthorID != "author1" {
		t.Errorf("expected AuthorID 'author1', got %q", msg.AuthorID)
	}
	if msg.AuthorName != "Author One" {
		t.Errorf("expected AuthorName 'Author One', got %q", msg.AuthorName)
	}
	if msg.Role != message.RoleAssistant {
		t.Errorf("expected Role 'assistant', got %q", msg.Role)
	}
}

func TestResponse_Update_OverrideWithNewValues(t *testing.T) {
	resp := &message.Response{}

	// First update
	update1 := &message.ResponseUpdate{
		MessageID:  "msg1",
		AuthorID:   "author1",
		AuthorName: "Author One",
		Role:       message.RoleAssistant,
		Contents:   message.Contents{&message.TextContent{Text: "First"}},
	}
	resp.Update(update1)

	// Second update with different message ID - creates new message
	update2 := &message.ResponseUpdate{
		MessageID:  "msg2",
		AuthorID:   "author2",
		AuthorName: "Author Two",
		Role:       message.RoleUser,
		Contents:   message.Contents{&message.TextContent{Text: "Second"}},
	}
	resp.Update(update2)

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}

	msg := resp.Messages[1]
	if msg.AuthorID != "author2" {
		t.Errorf("expected AuthorID 'author2', got %q", msg.AuthorID)
	}
	if msg.AuthorName != "Author Two" {
		t.Errorf("expected AuthorName 'Author Two', got %q", msg.AuthorName)
	}
	if msg.Role != message.RoleUser {
		t.Errorf("expected Role 'user', got %q", msg.Role)
	}
}

func TestResponse_Update_MultipleMessages(t *testing.T) {
	resp := &message.Response{}

	// Create first message
	update1 := &message.ResponseUpdate{
		MessageID: "msg1",
		Contents:  message.Contents{&message.TextContent{Text: "Message 1"}},
	}
	resp.Update(update1)

	// Create second message
	update2 := &message.ResponseUpdate{
		MessageID: "msg2",
		Contents:  message.Contents{&message.TextContent{Text: "Message 2"}},
	}
	resp.Update(update2)

	// Create third message
	update3 := &message.ResponseUpdate{
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

func TestMessage_Clone_ClonesAdditionalProperties(t *testing.T) {
	original := &message.Message{
		AdditionalProperties: map[string]any{"k": "v"},
	}

	cloned := original.Clone()
	if cloned == nil {
		t.Fatal("expected cloned message")
	}
	if cloned.AdditionalProperties["k"] != "v" {
		t.Fatalf("expected cloned additional property value 'v', got %v", cloned.AdditionalProperties["k"])
	}

	cloned.AdditionalProperties["k"] = "changed"
	if original.AdditionalProperties["k"] != "v" {
		t.Fatalf("expected original additional properties to remain unchanged, got %v", original.AdditionalProperties["k"])
	}
}
