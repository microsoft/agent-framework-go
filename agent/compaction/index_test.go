// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"context"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/message"
)

func TestMessageIndex_CreateBasicGroups(t *testing.T) {
	tests := []struct {
		name string
		msg  *message.Message
		kind compaction.GroupKind
	}{
		{name: "system", msg: textMessage(message.RoleSystem, "system"), kind: compaction.GroupKindSystem},
		{name: "user", msg: textMessage(message.RoleUser, "hello"), kind: compaction.GroupKindUser},
		{name: "assistant", msg: textMessage(message.RoleAssistant, "hi"), kind: compaction.GroupKindAssistantText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := compaction.CreateMessageIndex([]*message.Message{tt.msg}, nil)
			if got := len(index.Groups); got != 1 {
				t.Fatalf("expected one group, got %d", got)
			}
			if got := index.Groups[0].Kind; got != tt.kind {
				t.Fatalf("unexpected group kind: got %v want %v", got, tt.kind)
			}
		})
	}
}

func TestMessageIndex_CountsStableToolContent(t *testing.T) {
	msg := &message.Message{
		Role: message.RoleUser,
		Contents: message.Contents{
			&message.HostedVectorStoreContent{VectorStoreID: "store"},
			&message.ImageGenerationToolResultContent{
				CallID:  "img",
				Outputs: message.Contents{&message.TextContent{Text: "pixels"}},
			},
			&message.WebSearchToolCallContent{CallID: "web", Queries: []string{"one", "two"}},
			&message.MCPServerToolResultContent{
				CallID:  "mcp",
				Outputs: message.Contents{&message.ErrorContent{Message: "bad"}},
			},
			&message.CodeInterpreterToolCallContent{
				CallID: "code",
				Inputs: message.Contents{&message.TextContent{Text: "x"}},
			},
			&message.RawContent{ContentHeader: message.ContentHeader{RawRepresentation: map[string]any{"opaque": "ignored"}}},
		},
	}

	index := compaction.CreateMessageIndex([]*message.Message{msg}, nil)
	if got, want := index.Groups[0].ByteCount, 34; got != want {
		t.Fatalf("ByteCount = %d, want %d", got, want)
	}
}

func TestMessageIndex_CreateMixedConversationGroupsCorrectly(t *testing.T) {
	for _, tt := range []struct {
		name     string
		messages []*message.Message
	}{
		{
			name: "text-only tool result",
			messages: []*message.Message{
				textMessage(message.RoleSystem, "You are helpful."),
				textMessage(message.RoleUser, "What's the weather?"),
				functionCallMessage("call1", "get_weather"),
				textMessage(message.RoleTool, "Sunny"),
				textMessage(message.RoleAssistant, "The weather is sunny!"),
			},
		},
		{
			name: "typed tool result",
			messages: []*message.Message{
				textMessage(message.RoleSystem, "system"),
				textMessage(message.RoleUser, "weather?"),
				functionCallMessage("call1", "get_weather"),
				functionResultMessage("call1", "Sunny"),
				textMessage(message.RoleAssistant, "sunny"),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			index := compaction.CreateMessageIndex(tt.messages, nil)
			got := make([]compaction.GroupKind, len(index.Groups))
			for i, group := range index.Groups {
				got[i] = group.Kind
			}
			want := []compaction.GroupKind{
				compaction.GroupKindSystem,
				compaction.GroupKindUser,
				compaction.GroupKindToolCall,
				compaction.GroupKindAssistantText,
			}
			if !slices.Equal(got, want) {
				t.Fatalf("unexpected group kinds: got %v want %v", got, want)
			}
			if got := index.Groups[2].MessageCount; got != 2 {
				t.Fatalf("expected tool call group to contain call and result, got %d", got)
			}
		})
	}
}

func TestMessageIndex_IncludedAndAllMessagesRespectExclusions(t *testing.T) {
	msgs := []*message.Message{
		textMessage(message.RoleUser, "first"),
		textMessage(message.RoleAssistant, "response"),
		textMessage(message.RoleUser, "second"),
	}
	index := compaction.CreateMessageIndex(msgs, nil)
	index.Groups[1].IsExcluded = true

	if got, want := index.IncludedMessages(), []*message.Message{msgs[0], msgs[2]}; !slices.Equal(got, want) {
		t.Fatalf("unexpected included message references: got %#v want %#v", got, want)
	}
	if got, want := messageTexts(index.IncludedMessages()), []string{"first", "second"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
	if got, want := messageTexts(index.AllMessages()), []string{"first", "response", "second"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected all messages: got %v want %v", got, want)
	}
}

func TestMessageIndex_ClassifiesStrategySummaryMessages(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(2), nil)
	strategy := &compaction.SummarizationStrategy{
		Trigger:                compaction.GroupsExceed(2),
		Summarizer:             compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) { return "older context", nil }),
		MinimumPreservedGroups: new(1),
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	classified := compaction.CreateMessageIndex([]*message.Message{index.IncludedMessages()[0]}, nil)
	if got := classified.Groups[0].Kind; got != compaction.GroupKindSummary {
		t.Fatalf("unexpected summary group kind: got %v want %v", got, compaction.GroupKindSummary)
	}

	ordinary := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleAssistant, "ordinary")}, nil)
	if got := ordinary.Groups[0].Kind; got != compaction.GroupKindAssistantText {
		t.Fatalf("unexpected ordinary group kind: got %v want %v", got, compaction.GroupKindAssistantText)
	}
}

func TestMessageIndex_TurnIndicesAndCounts(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "system"),
		textMessage(message.RoleUser, "q1"),
		textMessage(message.RoleAssistant, "a1"),
		textMessage(message.RoleUser, "q2"),
		textMessage(message.RoleAssistant, "a2"),
		textMessage(message.RoleUser, "q3"),
	}, nil)

	assertNilTurn(t, index.Groups[0])
	assertTurn(t, index.Groups[1], 1)
	assertTurn(t, index.Groups[2], 1)
	assertTurn(t, index.Groups[3], 2)
	assertTurn(t, index.Groups[4], 2)
	assertTurn(t, index.Groups[5], 3)
	if got := index.TotalTurnCount(); got != 3 {
		t.Fatalf("expected total turn count 3, got %d", got)
	}

	index.Groups[1].IsExcluded = true
	index.Groups[2].IsExcluded = true
	if got := index.IncludedTurnCount(); got != 2 {
		t.Fatalf("expected included turn count 2 after excluding turn 1, got %d", got)
	}

	turn2 := index.TurnGroups(2)
	if got := len(turn2); got != 2 {
		t.Fatalf("expected two groups for turn 2, got %d", got)
	}
	if turn2[0].Kind != compaction.GroupKindUser || turn2[1].Kind != compaction.GroupKindAssistantText {
		t.Fatalf("unexpected turn groups: %v, %v", turn2[0].Kind, turn2[1].Kind)
	}
}

func TestMessageIndex_UpdateBehaviors(t *testing.T) {
	msgs := []*message.Message{
		textMessage(message.RoleUser, "q1"),
		textMessage(message.RoleAssistant, "a1"),
	}
	index := compaction.CreateMessageIndex(msgs, nil)
	if got := len(index.Groups); got != 2 {
		t.Fatalf("expected initial index to contain 2 groups, got %d", got)
	}
	if got := index.RawMessageCount(); got != 2 {
		t.Fatalf("expected initial raw message count 2, got %d", got)
	}
	index.Groups[0].IsExcluded = true
	index.Groups[0].ExcludeReason = "test"

	msgs = append(msgs, textMessage(message.RoleUser, "q2"), textMessage(message.RoleAssistant, "a2"))
	index.Update(msgs)
	if got := len(index.Groups); got != 4 {
		t.Fatalf("expected appended update to produce 4 groups, got %d", got)
	}
	if got := index.RawMessageCount(); got != 4 {
		t.Fatalf("expected appended raw message count 4, got %d", got)
	}
	if got := index.Groups[2].Kind; got != compaction.GroupKindUser {
		t.Fatalf("unexpected appended user group kind: got %v want %v", got, compaction.GroupKindUser)
	}
	if got := index.Groups[3].Kind; got != compaction.GroupKindAssistantText {
		t.Fatalf("unexpected appended assistant group kind: got %v want %v", got, compaction.GroupKindAssistantText)
	}
	if !index.Groups[0].IsExcluded || index.Groups[0].ExcludeReason != "test" {
		t.Fatal("expected existing exclusion state to be preserved")
	}

	index.Update(msgs)
	if got := len(index.Groups); got != 4 {
		t.Fatalf("expected no-op update to keep 4 groups, got %d", got)
	}

	index.Update([]*message.Message{textMessage(message.RoleUser, "replacement")})
	if got := len(index.Groups); got != 1 {
		t.Fatalf("expected rebuild after replacement, got %d groups", got)
	}
	if index.Groups[0].IsExcluded {
		t.Fatal("expected rebuild to clear exclusion state")
	}

	index.Update(nil)
	if got := len(index.Groups); got != 0 {
		t.Fatalf("expected empty update to clear groups, got %d", got)
	}
}

func TestMessageIndex_UpdatePreservesStateFromCompactedProjection(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(3), nil)
	strategy := &compaction.SummarizationStrategy{
		Trigger:                compaction.GroupsExceed(2),
		Summarizer:             compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) { return "older context", nil }),
		MinimumPreservedGroups: new(2),
		SummarizationPrompt:    new("summarize"),
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	messages := slices.Clone(index.IncludedMessages())
	messages = append(messages, textMessage(message.RoleUser, "u4"), textMessage(message.RoleAssistant, "a4"))
	index.Update(messages)

	if got, want := index.TotalGroupCount(), 9; got != want {
		t.Fatalf("expected compacted state to be preserved, got %d groups want %d", got, want)
	}
	if got, want := messageTexts(index.IncludedMessages()), []string{"[Summary]\nolder context", "u3", "a3", "u4", "a4"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
	if !index.Groups[1].IsExcluded || !index.Groups[2].IsExcluded {
		t.Fatal("expected previous exclusion state to be preserved")
	}
}

func TestMessageIndex_InsertAndAddGroupComputeCounts(t *testing.T) {
	t.Run("nil turn", func(t *testing.T) {
		index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Q1")}, nil)
		inserted := index.InsertGroup(0, compaction.GroupKindAssistantText, []*message.Message{textMessage(message.RoleAssistant, "Hello")}, nil)
		if inserted.ByteCount != 5 || inserted.TokenCount != 1 {
			t.Fatalf("unexpected inserted counts: bytes=%d tokens=%d", inserted.ByteCount, inserted.TokenCount)
		}
		assertNilTurn(t, inserted)
	})

	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "q1")}, nil)
	turnIndex := 1

	inserted := index.InsertGroup(0, compaction.GroupKindAssistantText, []*message.Message{textMessage(message.RoleAssistant, "Hello")}, &turnIndex)
	if index.Groups[0] != inserted {
		t.Fatal("expected inserted group at index 0")
	}
	if inserted.ByteCount != 5 || inserted.TokenCount != 1 {
		t.Fatalf("unexpected inserted counts: bytes=%d tokens=%d", inserted.ByteCount, inserted.TokenCount)
	}
	assertTurn(t, inserted, 1)

	added := index.AddGroup(compaction.GroupKindAssistantText, []*message.Message{textMessage(message.RoleAssistant, "Appended")}, &turnIndex)
	if index.Groups[len(index.Groups)-1] != added {
		t.Fatal("expected added group at end")
	}
}

func TestMessageIndex_ByteAndTokenCounts(t *testing.T) {
	msgs := []*message.Message{
		textMessage(message.RoleUser, "cafe"),
		{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.FunctionCallContent{CallID: "c1", Name: "fn", Arguments: `{"city":"Seattle"}`},
			},
		},
	}

	index := compaction.CreateMessageIndex(msgs, nil)
	byteCount := index.Groups[0].ByteCount + index.Groups[1].ByteCount
	if byteCount <= 4 {
		t.Fatalf("expected function call content to contribute bytes, got %d", byteCount)
	}

	counter := tokenCounterFunc(func(text string) int { return len(slices.Collect(splitWords(text))) })
	withReasoning := []*message.Message{{
		Role: message.RoleAssistant,
		Contents: []message.Content{
			&message.TextContent{Text: "hello world"},
			&message.TextReasoningContent{Text: "deep thought", ProtectedData: "hidden data"},
		},
	}}
	index = compaction.CreateMessageIndex(withReasoning, counter)
	if got, want := index.Groups[0].TokenCount, 6; got != want {
		t.Fatalf("unexpected token count: got %d want %d", got, want)
	}

	index = compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "hello world test")}, counter)
	if got := len(index.Groups); got != 1 {
		t.Fatalf("expected one group, got %d", got)
	}
	if index.TokenCounter == nil {
		t.Fatal("expected token counter to be retained")
	}
	if got, want := index.Groups[0].TokenCount, 3; got != want {
		t.Fatalf("expected tokenizer count 3, got %d", got)
	}
}

func TestMessageIndex_ReasoningToolCallGrouping(t *testing.T) {
	reasoning := &message.Message{Role: message.RoleAssistant, Contents: []message.Content{&message.TextReasoningContent{Text: "think"}}}
	toolCall := functionCallMessage("c1", "search")
	toolResult := functionResultMessage("c1", "results")

	index := compaction.CreateMessageIndex([]*message.Message{reasoning, toolCall, toolResult}, nil)
	if got := len(index.Groups); got != 1 {
		t.Fatalf("expected one tool call group, got %d", got)
	}
	if index.Groups[0].Kind != compaction.GroupKindToolCall || index.Groups[0].MessageCount != 3 {
		t.Fatalf("unexpected reasoning tool call group: kind=%v count=%d", index.Groups[0].Kind, index.Groups[0].MessageCount)
	}
	if got, want := index.Groups[0].Messages, []*message.Message{reasoning, toolCall, toolResult}; !slices.Equal(got, want) {
		t.Fatalf("unexpected reasoning tool call message references: got %#v want %#v", got, want)
	}

	index = compaction.CreateMessageIndex([]*message.Message{reasoning, textMessage(message.RoleUser, "hello")}, nil)
	if got, want := []compaction.GroupKind{index.Groups[0].Kind, index.Groups[1].Kind}, []compaction.GroupKind{compaction.GroupKindAssistantText, compaction.GroupKindUser}; !slices.Equal(got, want) {
		t.Fatalf("unexpected non-tool reasoning grouping: got %v want %v", got, want)
	}
}

func TestMessageIndex_StandaloneToolMessageFallsBackToAssistantText(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleTool, "orphan")}, nil)
	if got := len(index.Groups); got != 1 {
		t.Fatalf("expected one orphan tool group, got %d", got)
	}
	if got := index.Groups[0].Kind; got != compaction.GroupKindAssistantText {
		t.Fatalf("unexpected orphan tool kind: got %v want %v", got, compaction.GroupKindAssistantText)
	}
}

func functionCallMessage(callID, name string) *message.Message {
	return &message.Message{
		Role: message.RoleAssistant,
		Contents: []message.Content{
			&message.FunctionCallContent{CallID: callID, Name: name},
		},
	}
}

func functionResultMessage(callID string, result any) *message.Message {
	return &message.Message{
		Role: message.RoleTool,
		Contents: []message.Content{
			&message.FunctionResultContent{CallID: callID, Result: result},
		},
	}
}

func assertTurn(t *testing.T, group *compaction.MessageGroup, want int) {
	t.Helper()
	if group.TurnIndex == nil || *group.TurnIndex != want {
		t.Fatalf("unexpected turn index: got %v want %d", group.TurnIndex, want)
	}
}

func assertNilTurn(t *testing.T, group *compaction.MessageGroup) {
	t.Helper()
	if group.TurnIndex != nil {
		t.Fatalf("expected nil turn index, got %d", *group.TurnIndex)
	}
}

func splitWords(text string) func(func(string) bool) {
	return func(yield func(string) bool) {
		start := -1
		for i, r := range text {
			if r == ' ' || r == '\t' || r == '\n' {
				if start >= 0 && !yield(text[start:i]) {
					return
				}
				start = -1
				continue
			}
			if start < 0 {
				start = i
			}
		}
		if start >= 0 {
			yield(text[start:])
		}
	}
}

// A user re-sending an identical short message (or any repeated content) must
// not cause Update to drop the turns appended since the previous update. The
// processed-prefix boundary must be located positionally, not by matching the
// last occurrence of the previous message's content.
func TestMessageIndex_Update_RepeatedContentDoesNotDropMessages(t *testing.T) {
	turn1 := []*message.Message{
		textMessage(message.RoleUser, "hi"),
		textMessage(message.RoleAssistant, "hello"),
		textMessage(message.RoleUser, "continue"),
	}
	index := &compaction.MessageIndex{}
	index.Update(turn1)
	if got, want := messageTexts(index.AllMessages()), []string{"hi", "hello", "continue"}; !slices.Equal(got, want) {
		t.Fatalf("after turn 1: got %v, want %v", got, want)
	}

	// Turn 2: the assistant replies, then the user repeats "continue" verbatim.
	turn2 := append(slices.Clone(turn1),
		textMessage(message.RoleAssistant, "sure"),
		textMessage(message.RoleUser, "continue"),
	)
	index.Update(turn2)
	if got, want := messageTexts(index.AllMessages()), []string{"hi", "hello", "continue", "sure", "continue"}; !slices.Equal(got, want) {
		t.Fatalf("after turn 2 (repeated \"continue\"): got %v, want %v — appended turns were dropped", got, want)
	}
}

func assertGroupKinds(t *testing.T, index *compaction.MessageIndex, want ...compaction.GroupKind) {
	t.Helper()
	got := make([]compaction.GroupKind, len(index.Groups))
	for i, group := range index.Groups {
		got[i] = group.Kind
	}
	if !slices.Equal(got, want) {
		t.Fatalf("group kinds = %v, want %v", got, want)
	}
}

func TestMessageIndex_CreateEmptyListReturnsEmptyGroups(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{}, nil)
	if len(index.Groups) != 0 {
		t.Fatalf("groups = %v, want empty", index.Groups)
	}
}

func TestMessageIndex_CreateSystemMessageCreatesSystemGroup(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleSystem, "You are helpful.")}, nil)
	assertGroupKinds(t, index, compaction.GroupKindSystem)
	if len(index.Groups[0].Messages) != 1 {
		t.Fatalf("system group has %d messages, want 1", len(index.Groups[0].Messages))
	}
}

func TestMessageIndex_CreateAssistantTextMessageCreatesAssistantTextGroup(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleAssistant, "Hi there!")}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText)
}

func TestMessageIndex_CreateToolCallWithResultsCreatesAtomicGroup(t *testing.T) {
	call := &message.Message{Role: message.RoleAssistant, Contents: message.Contents{
		&message.FunctionCallContent{CallID: "call1", Name: "get_weather", Arguments: `{"city":"Seattle"}`},
	}}
	result := functionResultMessage("call1", "Sunny, 72°F")
	index := compaction.CreateMessageIndex([]*message.Message{call, result}, nil)
	assertGroupKinds(t, index, compaction.GroupKindToolCall)
	if got, want := index.Groups[0].Messages, []*message.Message{call, result}; !slices.Equal(got, want) {
		t.Fatalf("atomic group messages = %#v, want %#v", got, want)
	}
}

func TestMessageIndex_CreateToolCallWithTextCreatesAtomicGroup(t *testing.T) {
	call := &message.Message{Role: message.RoleAssistant, Contents: message.Contents{
		&message.FunctionCallContent{CallID: "call1", Name: "get_weather", Arguments: `{"city":"Seattle"}`},
	}}
	result := &message.Message{Role: message.RoleTool, Contents: message.Contents{
		&message.TextContent{Text: "Sunny, 72°F"},
		&message.FunctionResultContent{CallID: "call1", Result: "Sunny, 72°F"},
	}}
	index := compaction.CreateMessageIndex([]*message.Message{call, result}, nil)
	assertGroupKinds(t, index, compaction.GroupKindToolCall)
	if got, want := index.Groups[0].Messages, []*message.Message{call, result}; !slices.Equal(got, want) {
		t.Fatalf("atomic group messages = %#v, want %#v", got, want)
	}
}

func TestMessageIndex_CreateMultipleToolResultsGroupsAllWithAssistant(t *testing.T) {
	call := &message.Message{Role: message.RoleAssistant, Contents: message.Contents{
		&message.FunctionCallContent{CallID: "call1", Name: "get_weather"},
		&message.FunctionCallContent{CallID: "call2", Name: "get_time"},
	}}
	index := compaction.CreateMessageIndex([]*message.Message{
		call,
		textMessage(message.RoleTool, "Sunny"),
		textMessage(message.RoleTool, "3:00 PM"),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindToolCall)
	if len(index.Groups[0].Messages) != 3 {
		t.Fatalf("tool group has %d messages, want 3", len(index.Groups[0].Messages))
	}
}

func TestMessageIndex_GetAllMessagesIncludesExcludedGroups(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "First"),
		textMessage(message.RoleAssistant, "Response"),
	}, nil)
	index.Groups[0].IsExcluded = true
	if got := len(index.AllMessages()); got != 2 {
		t.Fatalf("AllMessages has %d messages, want 2", got)
	}
}

func TestMessageIndex_IncludedGroupCountReflectsExclusions(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "A"),
		textMessage(message.RoleAssistant, "B"),
		textMessage(message.RoleUser, "C"),
	}, nil)
	index.Groups[1].IsExcluded = true
	if index.IncludedGroupCount() != 2 || index.IncludedMessageCount() != 2 {
		t.Fatalf("included groups/messages = %d/%d, want 2/2", index.IncludedGroupCount(), index.IncludedMessageCount())
	}
}

func TestMessageIndex_CreateSummaryMessageCreatesSummaryGroup(t *testing.T) {
	summary := textMessage(message.RoleAssistant, "[Summary of earlier conversation]: key facts...")
	summary.AdditionalProperties = map[string]any{"_is_summary": true}
	index := compaction.CreateMessageIndex([]*message.Message{summary}, nil)
	assertGroupKinds(t, index, compaction.GroupKindSummary)
	if index.Groups[0].Messages[0] != summary {
		t.Fatal("summary message reference was not preserved")
	}
}

func TestMessageIndex_CreateSummaryAmongOtherMessagesGroupsCorrectly(t *testing.T) {
	summary := textMessage(message.RoleAssistant, "[Summary]: previous context")
	summary.AdditionalProperties = map[string]any{"_is_summary": true}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "You are helpful."),
		summary,
		textMessage(message.RoleUser, "Continue..."),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindSystem, compaction.GroupKindSummary, compaction.GroupKindUser)
}

func TestMessageIndex_CreateComputesByteCountUtf8(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Hello")}, nil)
	if got := index.Groups[0].ByteCount; got != 5 {
		t.Fatalf("ByteCount = %d, want 5", got)
	}
}

func TestMessageIndex_CreateComputesByteCountMultiByteChars(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "café")}, nil)
	if got := index.Groups[0].ByteCount; got != 5 {
		t.Fatalf("ByteCount = %d, want 5", got)
	}
}

func TestMessageIndex_CreateComputesByteCountMultipleMessagesInGroup(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		functionCallMessage("call1", "fn"),
		textMessage(message.RoleTool, "OK"),
	}, nil)
	if len(index.Groups) != 1 {
		t.Fatalf("group count = %d, want 1", len(index.Groups))
	}
	if group := index.Groups[0]; group.MessageCount != 2 || group.ByteCount != 9 {
		t.Fatalf("tool group messages/bytes = %d/%d, want 2/9", group.MessageCount, group.ByteCount)
	}
}

func TestMessageIndex_CreateDefaultTokenCountIsHeuristic(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Hello world test data!")}, nil)
	if group := index.Groups[0]; group.ByteCount != 22 || group.TokenCount != 22/4 {
		t.Fatalf("bytes/tokens = %d/%d, want 22/5", group.ByteCount, group.TokenCount)
	}
}

func TestMessageIndex_CreateNonTextContentHasAccurateCounts(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		functionCallMessage("call1", "get_weather"),
		textMessage(message.RoleTool, ""),
	}, nil)
	group := index.Groups[0]
	if group.MessageCount != 2 || group.ByteCount != 16 || group.TokenCount != 4 {
		t.Fatalf("messages/bytes/tokens = %d/%d/%d, want 2/16/4", group.MessageCount, group.ByteCount, group.TokenCount)
	}
}

func TestMessageIndex_TotalAggregatesSumAllGroups(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "AAAA"),
		textMessage(message.RoleAssistant, "BBBB"),
	}, nil)
	index.Groups[0].IsExcluded = true
	if index.TotalGroupCount() != 2 || index.TotalMessageCount() != 2 || index.TotalByteCount() != 8 || index.TotalTokenCount() != 2 {
		t.Fatalf("totals groups/messages/bytes/tokens = %d/%d/%d/%d, want 2/2/8/2",
			index.TotalGroupCount(), index.TotalMessageCount(), index.TotalByteCount(), index.TotalTokenCount())
	}
}

func TestMessageIndex_IncludedAggregatesExcludeMarkedGroups(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "AAAA"),
		textMessage(message.RoleAssistant, "BBBB"),
		textMessage(message.RoleUser, "CCCC"),
	}, nil)
	index.Groups[0].IsExcluded = true
	if index.TotalGroupCount() != 3 || index.IncludedGroupCount() != 2 {
		t.Fatalf("total/included groups = %d/%d, want 3/2", index.TotalGroupCount(), index.IncludedGroupCount())
	}
	if index.TotalMessageCount() != 3 || index.IncludedMessageCount() != 2 {
		t.Fatalf("total/included messages = %d/%d, want 3/2", index.TotalMessageCount(), index.IncludedMessageCount())
	}
	if index.TotalByteCount() != 12 || index.IncludedByteCount() != 8 {
		t.Fatalf("total/included bytes = %d/%d, want 12/8", index.TotalByteCount(), index.IncludedByteCount())
	}
	if index.TotalTokenCount() != 3 || index.IncludedTokenCount() != 2 {
		t.Fatalf("total/included tokens = %d/%d, want 3/2", index.TotalTokenCount(), index.IncludedTokenCount())
	}
}

func TestMessageIndex_ToolCallGroupAggregatesAcrossMessages(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		functionCallMessage("call1", "fn"),
		textMessage(message.RoleTool, "OK"),
	}, nil)
	if len(index.Groups) != 1 {
		t.Fatalf("group count = %d, want 1", len(index.Groups))
	}
	if group := index.Groups[0]; group.MessageCount != 2 || group.ByteCount != 9 {
		t.Fatalf("tool group messages/bytes = %d/%d, want 2/9", group.MessageCount, group.ByteCount)
	}
	if index.TotalGroupCount() != 1 || index.TotalMessageCount() != 2 {
		t.Fatalf("total groups/messages = %d/%d, want 1/2", index.TotalGroupCount(), index.TotalMessageCount())
	}
}

func TestMessageIndex_CreateAssignsTurnIndicesSingleTurn(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "You are helpful."),
		textMessage(message.RoleUser, "Hello"),
		textMessage(message.RoleAssistant, "Hi!"),
	}, nil)
	assertNilTurn(t, index.Groups[0])
	assertTurn(t, index.Groups[1], 1)
	assertTurn(t, index.Groups[2], 1)
	if index.TotalTurnCount() != 1 || index.IncludedTurnCount() != 1 {
		t.Fatalf("total/included turns = %d/%d, want 1/1", index.TotalTurnCount(), index.IncludedTurnCount())
	}
}

func TestMessageIndex_CreateTurnSpansToolCallGroups(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "What's the weather?"),
		functionCallMessage("call1", "get_weather"),
		textMessage(message.RoleTool, "Sunny"),
		textMessage(message.RoleAssistant, "The weather is sunny!"),
	}, nil)
	if len(index.Groups) != 3 {
		t.Fatalf("group count = %d, want 3", len(index.Groups))
	}
	for _, group := range index.Groups {
		assertTurn(t, group, 1)
	}
	if got := index.TotalTurnCount(); got != 1 {
		t.Fatalf("TotalTurnCount = %d, want 1", got)
	}
}

func TestMessageIndex_GetTurnGroupsReturnsGroupsForSpecificTurn(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "System."),
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
	}, nil)
	for _, turn := range []int{1, 2} {
		groups := index.TurnGroups(turn)
		if len(groups) != 2 {
			t.Fatalf("turn %d has %d groups, want 2", turn, len(groups))
		}
		if groups[0].Kind != compaction.GroupKindUser || groups[1].Kind != compaction.GroupKindAssistantText {
			t.Fatalf("turn %d kinds = %v/%v, want user/assistant", turn, groups[0].Kind, groups[1].Kind)
		}
	}
}

func TestMessageIndex_IncludedTurnCountReflectsExclusions(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
	}, nil)
	index.Groups[0].IsExcluded = true
	index.Groups[1].IsExcluded = true
	if index.TotalTurnCount() != 2 || index.IncludedTurnCount() != 1 {
		t.Fatalf("total/included turns = %d/%d, want 2/1", index.TotalTurnCount(), index.IncludedTurnCount())
	}
}

func TestMessageIndex_TotalTurnCountZeroWhenNoUserMessages(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleSystem, "System.")}, nil)
	if index.TotalTurnCount() != 0 || index.IncludedTurnCount() != 0 {
		t.Fatalf("total/included turns = %d/%d, want 0/0", index.TotalTurnCount(), index.IncludedTurnCount())
	}
}

func TestMessageIndex_IncludedTurnCountPartialExclusionStillCountsTurn(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
	}, nil)
	index.Groups[1].IsExcluded = true
	if index.TotalTurnCount() != 1 || index.IncludedTurnCount() != 1 {
		t.Fatalf("total/included turns = %d/%d, want 1/1", index.TotalTurnCount(), index.IncludedTurnCount())
	}
}

func TestMessageIndex_UpdateNoOpWhenNoNewMessages(t *testing.T) {
	messages := []*message.Message{textMessage(message.RoleUser, "Q1")}
	index := compaction.CreateMessageIndex(messages, nil)
	originalCount := len(index.Groups)
	index.Update(messages)
	if got := len(index.Groups); got != originalCount {
		t.Fatalf("group count = %d, want unchanged %d", got, originalCount)
	}
}

func TestMessageIndex_UpdateRebuildsWhenMessagesShrink(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if len(index.Groups) != 3 {
		t.Fatalf("initial group count = %d, want 3", len(index.Groups))
	}
	index.Groups[0].IsExcluded = true
	index.Update([]*message.Message{textMessage(message.RoleUser, "Q2")})
	if len(index.Groups) != 1 {
		t.Fatalf("rebuilt group count = %d, want 1", len(index.Groups))
	}
	if index.Groups[0].IsExcluded || index.RawMessageCount() != 1 {
		t.Fatalf("rebuilt excluded/raw = %v/%d, want false/1", index.Groups[0].IsExcluded, index.RawMessageCount())
	}
}

func TestMessageIndex_UpdateWithEmptyListClearsGroups(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
	}, nil)
	if len(index.Groups) != 2 {
		t.Fatalf("initial group count = %d, want 2", len(index.Groups))
	}
	index.Update([]*message.Message{})
	if len(index.Groups) != 0 || index.TotalTurnCount() != 0 || index.RawMessageCount() != 0 {
		t.Fatalf("cleared groups/turns/raw = %d/%d/%d, want 0/0/0", len(index.Groups), index.TotalTurnCount(), index.RawMessageCount())
	}
}

func TestMessageIndex_UpdateRebuildsWhenLastProcessedMessageNotFound(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
	}, nil)
	if len(index.Groups) != 2 {
		t.Fatalf("initial group count = %d, want 2", len(index.Groups))
	}
	index.Groups[0].IsExcluded = true
	index.Update([]*message.Message{
		textMessage(message.RoleUser, "X1"),
		textMessage(message.RoleAssistant, "X2"),
		textMessage(message.RoleUser, "X3"),
	})
	if len(index.Groups) != 3 || index.RawMessageCount() != 3 {
		t.Fatalf("rebuilt groups/raw = %d/%d, want 3/3", len(index.Groups), index.RawMessageCount())
	}
	for i, group := range index.Groups {
		if group.IsExcluded {
			t.Fatalf("rebuilt group %d is excluded", i)
		}
	}
}

func TestMessageIndex_UpdatePreservesExistingGroupExclusionState(t *testing.T) {
	messages := []*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
	}
	index := compaction.CreateMessageIndex(messages, nil)
	index.Groups[0].IsExcluded = true
	index.Groups[0].ExcludeReason = "Test exclusion"
	messages = append(messages, textMessage(message.RoleUser, "Q2"))
	index.Update(messages)
	if !index.Groups[0].IsExcluded || index.Groups[0].ExcludeReason != "Test exclusion" || len(index.Groups) != 3 {
		t.Fatalf("updated exclusion/reason/count = %v/%q/%d, want true/Test exclusion/3",
			index.Groups[0].IsExcluded, index.Groups[0].ExcludeReason, len(index.Groups))
	}
}

func TestMessageIndex_InsertGroupInsertsAtSpecifiedIndex(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	inserted := index.InsertGroup(1, compaction.GroupKindSummary, []*message.Message{textMessage(message.RoleAssistant, "[Summary]")}, new(1))
	if len(index.Groups) != 3 || index.Groups[1] != inserted {
		t.Fatalf("inserted group not at position 1 in three groups: %#v", index.Groups)
	}
	if index.Groups[1].Kind != compaction.GroupKindSummary || index.Groups[1].Messages[0].String() != "[Summary]" {
		t.Fatalf("inserted kind/text = %v/%q", index.Groups[1].Kind, index.Groups[1].Messages[0].String())
	}
	assertTurn(t, inserted, 1)
}

func TestMessageIndex_AddGroupAppendsToEnd(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Q1")}, nil)
	added := index.AddGroup(compaction.GroupKindAssistantText, []*message.Message{textMessage(message.RoleAssistant, "Appended")}, new(1))
	if len(index.Groups) != 2 || index.Groups[1] != added {
		t.Fatalf("added group not at end of two groups: %#v", index.Groups)
	}
	if got := index.Groups[1].Messages[0].String(); got != "Appended" {
		t.Fatalf("appended text = %q, want Appended", got)
	}
}

func TestMessageIndex_ConstructorWithGroupsRestoresTurnIndex(t *testing.T) {
	groups := []*compaction.MessageGroup{
		{Kind: compaction.GroupKindUser, Messages: []*message.Message{textMessage(message.RoleUser, "Q1")}, MessageCount: 1, ByteCount: 2, TokenCount: 1, TurnIndex: new(1)},
		{Kind: compaction.GroupKindAssistantText, Messages: []*message.Message{textMessage(message.RoleAssistant, "A1")}, MessageCount: 1, ByteCount: 2, TokenCount: 1, TurnIndex: new(1)},
		{Kind: compaction.GroupKindUser, Messages: []*message.Message{textMessage(message.RoleUser, "Q2")}, MessageCount: 1, ByteCount: 2, TokenCount: 1, TurnIndex: new(2)},
	}
	index := compaction.NewMessageIndex(groups, nil)
	index.Update([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleUser, "Q3"),
	})
	last := index.Groups[len(index.Groups)-1]
	if last.Kind != compaction.GroupKindUser || last.TurnIndex == nil {
		t.Fatalf("last group kind/turn = %v/%v, want user/non-nil", last.Kind, last.TurnIndex)
	}
	assertTurn(t, last, 3)
}

func TestMessageIndex_ConstructorWithEmptyGroupsHandlesGracefully(t *testing.T) {
	index := compaction.NewMessageIndex([]*compaction.MessageGroup{}, nil)
	if len(index.Groups) != 0 {
		t.Fatalf("group count = %d, want 0", len(index.Groups))
	}
}

func TestMessageIndex_ConstructorWithGroupsWithoutTurnIndexSkipsRestore(t *testing.T) {
	group := &compaction.MessageGroup{
		Kind: compaction.GroupKindSystem, Messages: []*message.Message{textMessage(message.RoleSystem, "Be helpful")},
		MessageCount: 1, ByteCount: 10, TokenCount: 3,
	}
	index := compaction.NewMessageIndex([]*compaction.MessageGroup{group}, nil)
	if len(index.Groups) != 1 {
		t.Fatalf("group count = %d, want 1", len(index.Groups))
	}
}

func TestMessageIndex_InsertGroupWithTokenizerUsesTokenizer(t *testing.T) {
	counter := tokenCounterFunc(func(text string) int { return len(slices.Collect(splitWords(text))) })
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Hello")}, counter)
	inserted := index.InsertGroup(0, compaction.GroupKindAssistantText, []*message.Message{textMessage(message.RoleAssistant, "Hello world test message")}, nil)
	if inserted.TokenCount != 4 {
		t.Fatalf("inserted TokenCount = %d, want 4", inserted.TokenCount)
	}
}

func TestMessageIndex_CreateWithAssistantNonSummaryWithPropertiesFallsToAssistantText(t *testing.T) {
	assistant := textMessage(message.RoleAssistant, "Regular response")
	assistant.AdditionalProperties = map[string]any{"someOtherKey": "value"}
	index := compaction.CreateMessageIndex([]*message.Message{assistant}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText)
}

func TestMessageIndex_CreateWithSummaryPropertyFalseIsNotSummary(t *testing.T) {
	assistant := textMessage(message.RoleAssistant, "Not a summary")
	assistant.AdditionalProperties = map[string]any{"_is_summary": false}
	index := compaction.CreateMessageIndex([]*message.Message{assistant}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText)
}

func TestMessageIndex_CreateWithSummaryPropertyNonBoolIsNotSummary(t *testing.T) {
	assistant := textMessage(message.RoleAssistant, "Not a summary")
	assistant.AdditionalProperties = map[string]any{"_is_summary": "true"}
	index := compaction.CreateMessageIndex([]*message.Message{assistant}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText)
}

func TestMessageIndex_CreateWithSummaryPropertyNullValueIsNotSummary(t *testing.T) {
	assistant := textMessage(message.RoleAssistant, "Not a summary")
	assistant.AdditionalProperties = map[string]any{"_is_summary": nil}
	index := compaction.CreateMessageIndex([]*message.Message{assistant}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText)
}

func TestMessageIndex_CreateWithNoAdditionalPropertiesIsNotSummary(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleAssistant, "Plain response")}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText)
}

func TestMessageIndex_CreateGroupByteCountIncludesAllContentTypes(t *testing.T) {
	const (
		callID          = "call1"
		functionName    = "get_weather"
		arguments       = `{"city":"Seattle"}`
		result          = "Sunny"
		wantCallBytes   = len(callID) + len(functionName) + len(arguments)
		wantResultBytes = len(callID) + len(result)
		wantBytes       = wantCallBytes + wantResultBytes
	)
	messages := []*message.Message{
		{Role: message.RoleAssistant, Contents: message.Contents{
			&message.FunctionCallContent{CallID: callID, Name: functionName, Arguments: arguments},
		}},
		functionResultMessage(callID, result),
	}
	index := compaction.CreateMessageIndex(messages, nil)
	if got := len(index.Groups); got != 1 {
		t.Fatalf("group count = %d, want 1", got)
	}
	group := index.Groups[0]
	if group.ByteCount != wantBytes {
		t.Fatalf("ByteCount = %d, want %d", group.ByteCount, wantBytes)
	}
	if group.TokenCount <= 0 || group.TokenCount != wantBytes/4 {
		t.Fatalf("TokenCount = %d, want %d (> 0)", group.TokenCount, wantBytes/4)
	}
}

func reasoningMessage(text string) *message.Message {
	return &message.Message{Role: message.RoleAssistant, Contents: message.Contents{&message.TextReasoningContent{Text: text}}}
}

func TestMessageIndex_CreateMultipleReasoningBeforeToolCallGroupsAtomic(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		reasoningMessage("First thought"),
		reasoningMessage("Second thought"),
		functionCallMessage("c1", "search"),
		functionResultMessage("c1", "results"),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindToolCall)
	if got := index.Groups[0].MessageCount; got != 4 {
		t.Fatalf("MessageCount = %d, want 4", got)
	}
}

func TestMessageIndex_CreateReasoningNotFollowedByToolCallIsAssistantText(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		reasoningMessage("Thinking..."),
		textMessage(message.RoleUser, "Hello"),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText, compaction.GroupKindUser)
}

func TestMessageIndex_CreateReasoningAtEndOfConversationIsAssistantText(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Hello"),
		reasoningMessage("Thinking..."),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindUser, compaction.GroupKindAssistantText)
}

func TestMessageIndex_CreateToolCallFollowedByReasoningInTail(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		functionCallMessage("c1", "fn"),
		functionResultMessage("c1", "data"),
		reasoningMessage("Analyzing result..."),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindToolCall)
	if got := index.Groups[0].MessageCount; got != 3 {
		t.Fatalf("MessageCount = %d, want 3", got)
	}
}

func TestMessageIndex_CreateReasoningBetweenToolCallsGroupsCorrectly(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		reasoningMessage("Plan: call get_weather"),
		functionCallMessage("c1", "get_weather"),
		functionResultMessage("c1", "Sunny"),
		textMessage(message.RoleUser, "What else?"),
		reasoningMessage("Plan: call get_time"),
		functionCallMessage("c2", "get_time"),
		functionResultMessage("c2", "3 PM"),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindToolCall, compaction.GroupKindUser, compaction.GroupKindToolCall)
	if index.Groups[0].MessageCount != 3 || index.Groups[2].MessageCount != 3 {
		t.Fatalf("tool group counts = %d/%d, want 3/3", index.Groups[0].MessageCount, index.Groups[2].MessageCount)
	}
}

func TestMessageIndex_CreateReasoningFollowedByNonReasoningAssistantNotGrouped(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		reasoningMessage("Thinking..."),
		textMessage(message.RoleAssistant, "Here's my answer."),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText, compaction.GroupKindAssistantText)
}

func TestMessageIndex_CreateAssistantWithMixedReasoningAndTextNotGroupedAsReasoning(t *testing.T) {
	mixed := &message.Message{Role: message.RoleAssistant, Contents: message.Contents{
		&message.TextReasoningContent{Text: "Thinking"},
		&message.TextContent{Text: "And also speaking"},
	}}
	index := compaction.CreateMessageIndex([]*message.Message{
		mixed,
		functionCallMessage("c1", "fn"),
		functionResultMessage("c1", "data"),
	}, nil)
	assertGroupKinds(t, index, compaction.GroupKindAssistantText, compaction.GroupKindToolCall)
}

func TestMessageIndex_CreateEmptyContentsAssistantIsAssistantText(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		{Role: message.RoleAssistant, Contents: message.Contents{}},
		textMessage(message.RoleUser, "Hello"),
	}, nil)
	if len(index.Groups) != 2 || index.Groups[0].Kind != compaction.GroupKindAssistantText {
		t.Fatalf("empty assistant groups = %#v, want two groups starting with assistant text", index.Groups)
	}
}

func TestMessageIndex_UpdateIncrementallyAppendsReasoningToolCallGroup(t *testing.T) {
	messages := []*message.Message{
		textMessage(message.RoleUser, "Hello"),
		textMessage(message.RoleAssistant, "Hi!"),
	}
	index := compaction.CreateMessageIndex(messages, nil)
	if len(index.Groups) != 2 {
		t.Fatalf("initial group count = %d, want 2", len(index.Groups))
	}
	messages = append(messages,
		reasoningMessage("Let me search"),
		functionCallMessage("c1", "search"),
		functionResultMessage("c1", "found"),
	)
	index.Update(messages)
	assertGroupKinds(t, index, compaction.GroupKindUser, compaction.GroupKindAssistantText, compaction.GroupKindToolCall)
	if got := index.Groups[2].MessageCount; got != 3 {
		t.Fatalf("appended tool MessageCount = %d, want 3", got)
	}
}
