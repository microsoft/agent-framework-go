// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func TestMessageMerger_PreservesFirstSeenMessageOrder(t *testing.T) {
	responseID := "response"
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	merger := newMessageMerger()
	addTextUpdate(merger, responseID, "first", "message-1", now.Add(time.Minute))
	addTextUpdate(merger, responseID, "second", "message-2", time.Time{})
	addTextUpdate(merger, responseID, "third", "message-3", now.Add(-time.Minute))
	addTextUpdate(merger, responseID, "fourth", "message-4", now.Add(-time.Minute))

	response := merger.ComputeMerged(responseID, "", "")

	assertMessageTexts(t, response.Messages, "first", "second", "third", "fourth")
	if got := response.Messages[0].CreatedAt; !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("first message CreatedAt = %v, want %v", got, now.Add(time.Minute))
	}
	if got := response.Messages[2].CreatedAt; !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("third message CreatedAt = %v, want %v", got, now.Add(-time.Minute))
	}
}

func TestMessageMerger_KeepsResponsesContiguousInFirstSeenOrder(t *testing.T) {
	merger := newMessageMerger()

	addTextUpdate(merger, "response-1", "A1", "message-a1", time.Time{})
	addTextUpdate(merger, "response-2", "B1", "message-b1", time.Time{})
	addTextUpdate(merger, "response-1", "A2", "message-a2", time.Time{})
	addTextUpdate(merger, "response-2", "B2", "message-b2", time.Time{})

	response := merger.ComputeMerged("response-1", "", "")

	assertMessageTexts(t, response.Messages, "A1", "A2", "B1", "B2")
}

func TestMessageMerger_PreservesFunctionCallResultOrder(t *testing.T) {
	const (
		responseID = "response"
		callID     = "call"
	)

	merger := newMessageMerger()
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: responseID,
		MessageID:  "call-message",
		Role:       message.RoleAssistant,
		Contents:   []message.Content{&message.FunctionCallContent{CallID: callID, Name: "handoff"}},
	})
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: responseID,
		MessageID:  "result-message",
		Role:       message.RoleTool,
		CreatedAt:  time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Contents:   []message.Content{&message.FunctionResultContent{CallID: callID, Result: "Transferred."}},
	})

	response := merger.ComputeMerged(responseID, "", "")

	if len(response.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(response.Messages))
	}
	if _, ok := response.Messages[0].Contents[0].(*message.FunctionCallContent); !ok {
		t.Fatalf("first content = %T, want *message.FunctionCallContent", response.Messages[0].Contents[0])
	}
	if _, ok := response.Messages[1].Contents[0].(*message.FunctionResultContent); !ok {
		t.Fatalf("second content = %T, want *message.FunctionResultContent", response.Messages[1].Contents[0])
	}
}

func TestMessageMerger_PreservesIdentifierlessMessageOrder(t *testing.T) {
	const (
		responseID = "response"
		callID     = "call"
	)

	merger := newMessageMerger()
	addTextUpdate(merger, responseID, "before", "before-message", time.Time{})
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: responseID,
		Role:       message.RoleAssistant,
		Contents:   []message.Content{&message.FunctionCallContent{CallID: callID, Name: "handoff"}},
	})
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: responseID,
		MessageID:  "result-message",
		Role:       message.RoleTool,
		CreatedAt:  time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Contents:   []message.Content{&message.FunctionResultContent{CallID: callID, Result: "Transferred."}},
	})

	response := merger.ComputeMerged(responseID, "", "")

	if len(response.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(response.Messages))
	}
	if got := response.Messages[0].String(); got != "before" {
		t.Fatalf("first message = %q, want %q", got, "before")
	}
	if _, ok := response.Messages[1].Contents[0].(*message.FunctionCallContent); !ok {
		t.Fatalf("second content = %T, want *message.FunctionCallContent", response.Messages[1].Contents[0])
	}
	if _, ok := response.Messages[2].Contents[0].(*message.FunctionResultContent); !ok {
		t.Fatalf("third content = %T, want *message.FunctionResultContent", response.Messages[2].Contents[0])
	}
}

func TestMessageMerger_StampsZeroCreatedAtWithMaxTimestamp(t *testing.T) {
	responseID := "response"
	older := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)

	merger := newMessageMerger()
	// message-1 has the newest timestamp; message-2 has no timestamp; message-3 is older.
	// Without max-tracking, "last-wins" would stamp message-2 with `older` instead of `newer`.
	addTextUpdate(merger, responseID, "first", "message-1", newer)
	addTextUpdate(merger, responseID, "second", "message-2", time.Time{})
	addTextUpdate(merger, responseID, "third", "message-3", older)

	response := merger.ComputeMerged(responseID, "", "")

	assertMessageTexts(t, response.Messages, "first", "second", "third")
	if got := response.Messages[1].CreatedAt; !got.Equal(newer) {
		t.Fatalf("zero-timestamp message stamped with %v, want max timestamp %v", got, newer)
	}
}

func TestMessageMerger_SeparatesIdentifierlessSegments(t *testing.T) {
	merger := newMessageMerger()
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: "response",
		MessageID:  "message",
		Role:       message.RoleAssistant,
		Contents:   []message.Content{&message.TextContent{Text: "A"}},
	})
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: "response",
		Role:       message.RoleTool,
		Contents:   []message.Content{&message.TextContent{Text: "X"}},
	})
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: "response",
		MessageID:  "message",
		Role:       message.RoleAssistant,
		Contents:   []message.Content{&message.TextContent{Text: "B"}},
	})
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: "response",
		Role:       message.RoleTool,
		Contents:   []message.Content{&message.TextContent{Text: "Y"}},
	})

	response := merger.ComputeMerged("response", "", "")

	assertMessageTexts(t, response.Messages, "AB", "X", "Y")
}

func addTextUpdate(merger *messageMerger, responseID string, text string, messageID string, createdAt time.Time) {
	merger.AddUpdate(&agent.ResponseUpdate{
		ResponseID: responseID,
		MessageID:  messageID,
		Role:       message.RoleAssistant,
		CreatedAt:  createdAt,
		Contents:   []message.Content{&message.TextContent{Text: text}},
	})
}

func assertMessageTexts(t *testing.T, messages []*message.Message, want ...string) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("message count = %d, want %d", len(messages), len(want))
	}
	for i, msg := range messages {
		if got := msg.String(); got != want[i] {
			t.Fatalf("message[%d] = %q, want %q", i, got, want[i])
		}
	}
}
