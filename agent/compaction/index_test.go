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

func TestMessageIndex_CreateMixedConversationGroupsCorrectly(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "system"),
		textMessage(message.RoleUser, "weather?"),
		functionCallMessage("call1", "get_weather"),
		functionResultMessage("call1", "Sunny"),
		textMessage(message.RoleAssistant, "sunny"),
	}, nil)

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
}

func TestMessageIndex_IncludedAndAllMessagesRespectExclusions(t *testing.T) {
	msgs := []*message.Message{
		textMessage(message.RoleUser, "first"),
		textMessage(message.RoleAssistant, "response"),
		textMessage(message.RoleUser, "second"),
	}
	index := compaction.CreateMessageIndex(msgs, nil)
	index.Groups[1].IsExcluded = true

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
		MinimumPreservedGroups: 1,
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
	index.Groups[0].IsExcluded = true
	index.Groups[0].ExcludeReason = "test"

	msgs = append(msgs, textMessage(message.RoleUser, "q2"), textMessage(message.RoleAssistant, "a2"))
	index.Update(msgs)
	if got := len(index.Groups); got != 4 {
		t.Fatalf("expected appended update to produce 4 groups, got %d", got)
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
		MinimumPreservedGroups: 2,
		SummarizationPrompt:    "summarize",
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

	index = compaction.CreateMessageIndex([]*message.Message{reasoning, textMessage(message.RoleUser, "hello")}, nil)
	if got, want := []compaction.GroupKind{index.Groups[0].Kind, index.Groups[1].Kind}, []compaction.GroupKind{compaction.GroupKindAssistantText, compaction.GroupKindUser}; !slices.Equal(got, want) {
		t.Fatalf("unexpected non-tool reasoning grouping: got %v want %v", got, want)
	}
}

func TestMessageIndex_StandaloneToolMessageFallsBackToAssistantText(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleTool, "orphan")}, nil)
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
