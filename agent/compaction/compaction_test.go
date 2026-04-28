// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/message"
)

func TestMessageIndex_GroupsToolCallsAtomically(t *testing.T) {
	messages := []*message.Message{
		textMessage(message.RoleSystem, "system"),
		textMessage(message.RoleUser, "user"),
		{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.TextReasoningContent{Text: "thinking"},
			},
		},
		{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.FunctionCallContent{CallID: "call-1", Name: "search", Arguments: `{"q":"go"}`},
			},
		},
		{
			Role: message.RoleTool,
			Contents: []message.Content{
				&message.FunctionResultContent{CallID: "call-1", Result: "found"},
			},
		},
		textMessage(message.RoleAssistant, "done"),
	}

	index := compaction.CreateMessageIndex(messages, nil)

	gotKinds := make([]compaction.GroupKind, len(index.Groups))
	for i, group := range index.Groups {
		gotKinds[i] = group.Kind
	}
	wantKinds := []compaction.GroupKind{
		compaction.GroupKindSystem,
		compaction.GroupKindUser,
		compaction.GroupKindToolCall,
		compaction.GroupKindAssistantText,
	}
	if !slices.Equal(gotKinds, wantKinds) {
		t.Fatalf("unexpected group kinds: got %v want %v", gotKinds, wantKinds)
	}
	if got := index.Groups[2].MessageCount; got != 3 {
		t.Fatalf("expected tool-call group to contain reasoning, call, and result messages, got %d", got)
	}
	if index.Groups[2].TurnIndex == nil || *index.Groups[2].TurnIndex != 1 {
		t.Fatalf("expected tool-call group to belong to turn 1")
	}
}

func TestTruncationStrategy_ExcludesOldestGroups(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(3), nil)
	strategy := &compaction.TruncationStrategy{
		Trigger:                compaction.GroupsExceed(2),
		MinimumPreservedGroups: 2,
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	got := messageTexts(index.IncludedMessages())
	want := []string{"u3", "a3"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
}

func TestTruncationStrategy_ZeroValueUsesDefaults(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(17), nil)
	strategy := &compaction.TruncationStrategy{}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}
	if got, want := index.IncludedGroupCount(), 32; got != want {
		t.Fatalf("unexpected included group count: got %d want %d", got, want)
	}
}

func TestSlidingWindowStrategy_ExcludesOldestTurns(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(3), nil)
	strategy := &compaction.SlidingWindowStrategy{
		Trigger:               compaction.TurnsExceed(1),
		MinimumPreservedTurns: 1,
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	got := messageTexts(index.IncludedMessages())
	want := []string{"u3", "a3"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
}

func TestSlidingWindowStrategy_PreservesTurnZeroGroups(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleAssistant, "preface"),
		textMessage(message.RoleUser, "u1"),
		textMessage(message.RoleAssistant, "a1"),
		textMessage(message.RoleUser, "u2"),
		textMessage(message.RoleAssistant, "a2"),
	}, nil)
	strategy := &compaction.SlidingWindowStrategy{
		Trigger:               compaction.TurnsExceed(1),
		MinimumPreservedTurns: 1,
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	got := messageTexts(index.IncludedMessages())
	want := []string{"preface", "u2", "a2"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
}

func TestSlidingWindowStrategy_ZeroValueUsesDefaults(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(3), nil)
	strategy := &compaction.SlidingWindowStrategy{}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}
	got := messageTexts(index.IncludedMessages())
	want := []string{"u3", "a3"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
}

func TestToolResultStrategy_CollapsesOldToolGroups(t *testing.T) {
	messages := []*message.Message{
		textMessage(message.RoleUser, "u1"),
		{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.FunctionCallContent{CallID: "call-1", Name: "search"},
			},
		},
		{
			Role: message.RoleTool,
			Contents: []message.Content{
				&message.FunctionResultContent{CallID: "call-1", Result: "found 3 docs"},
			},
		},
		textMessage(message.RoleAssistant, "a1"),
		textMessage(message.RoleUser, "u2"),
		textMessage(message.RoleAssistant, "a2"),
	}
	index := compaction.CreateMessageIndex(messages, nil)
	strategy := &compaction.ToolResultStrategy{
		Trigger:                compaction.HasToolCalls(),
		MinimumPreservedGroups: 2,
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	got := messageTexts(index.IncludedMessages())
	want := []string{"u1", "[Tool Calls]\nsearch:\n  - found 3 docs", "a1", "u2", "a2"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %#v want %#v", got, want)
	}
	if !isSummaryMessage(index.IncludedMessages()[1]) {
		t.Fatal("expected collapsed tool result to be marked as summary")
	}
}

func TestToolResultStrategy_ZeroValueUsesDefaults(t *testing.T) {
	messages := make([]*message.Message, 0, 20)
	for i := 0; i < 9; i++ {
		callID := "call-" + string(rune('0'+i))
		messages = append(messages,
			textMessage(message.RoleUser, "u"+string(rune('0'+i))),
			&message.Message{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: callID, Name: "lookup"},
				},
			},
			&message.Message{
				Role: message.RoleTool,
				Contents: []message.Content{
					&message.FunctionResultContent{CallID: callID, Result: "ok"},
				},
			},
		)
	}
	index := compaction.CreateMessageIndex(messages, nil)
	strategy := &compaction.ToolResultStrategy{}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}
	if got, want := index.TotalGroupCount(), 19; got != want {
		t.Fatalf("unexpected total group count: got %d want %d", got, want)
	}
	if got, want := index.IncludedGroupCount(), 18; got != want {
		t.Fatalf("unexpected included group count: got %d want %d", got, want)
	}
	if !isSummaryMessage(index.IncludedMessages()[1]) {
		t.Fatal("expected first tool group to be summarized")
	}
}

func TestSummarizationStrategy_InsertsSummaryAndPreservesRecentGroups(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(3), nil)
	var summarized []string
	summarizer := compaction.SummarizerFunc(func(_ context.Context, messages []*message.Message) (string, error) {
		summarized = messageTexts(messages)
		return "older context", nil
	})
	strategy := &compaction.SummarizationStrategy{
		Trigger:                compaction.GroupsExceed(2),
		Summarizer:             summarizer,
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

	if want := []string{"summarize", "u1", "a1", "u2", "a2"}; !slices.Equal(summarized, want) {
		t.Fatalf("unexpected summarizer input: got %v want %v", summarized, want)
	}
	got := messageTexts(index.IncludedMessages())
	want := []string{"[Summary]\nolder context", "u3", "a3"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
	if !isSummaryMessage(index.IncludedMessages()[0]) {
		t.Fatal("expected inserted summary to be marked")
	}
}

func TestSummarizationStrategy_ZeroValueUsesDefaults(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(5), nil)
	var summarized []string
	strategy := &compaction.SummarizationStrategy{
		Summarizer: compaction.SummarizerFunc(func(_ context.Context, messages []*message.Message) (string, error) {
			summarized = messageTexts(messages)
			return "older context", nil
		}),
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}
	if got, want := len(summarized), 3; got != want {
		t.Fatalf("unexpected summarizer message count: got %d want %d", got, want)
	}
	if summarized[0] == "" {
		t.Fatal("expected default summarization prompt")
	}
	if got, want := index.IncludedGroupCount(), 9; got != want {
		t.Fatalf("unexpected included group count: got %d want %d", got, want)
	}
}

func TestSummarizationStrategy_ZeroValueWithoutSummarizerIsNoOp(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(5), nil)
	strategy := &compaction.SummarizationStrategy{}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if compacted {
		t.Fatal("expected no compaction without a summarizer")
	}
}

func TestSummarizationStrategy_RestoresGroupsWhenSummarizerFails(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(2), nil)
	expected := errors.New("summarizer failed")
	strategy := &compaction.SummarizationStrategy{
		Trigger:                compaction.GroupsExceed(2),
		Summarizer:             compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) { return "", expected }),
		MinimumPreservedGroups: 1,
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if compacted {
		t.Fatal("expected no compaction when summarizer fails")
	}
	if got := messageTexts(index.IncludedMessages()); !slices.Equal(got, []string{"u1", "a1", "u2", "a2"}) {
		t.Fatalf("expected groups to be restored, got %v", got)
	}
}

func TestSummarizationStrategy_PropagatesCancellation(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(2), nil)
	strategy := &compaction.SummarizationStrategy{
		Trigger:                compaction.GroupsExceed(2),
		Summarizer:             compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) { return "", context.Canceled }),
		MinimumPreservedGroups: 1,
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	if compacted {
		t.Fatal("expected no compaction when summarizer is canceled")
	}
	if got := messageTexts(index.IncludedMessages()); !slices.Equal(got, []string{"u1", "a1", "u2", "a2"}) {
		t.Fatalf("expected groups to be restored, got %v", got)
	}
}

func TestNewProvider_CompactsAndPersistsIndex(t *testing.T) {
	session := agent.NewSession("session")
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{
			Trigger:                compaction.GroupsExceed(2),
			MinimumPreservedGroups: 2,
		},
		SourceID: "compaction-test",
	})

	compactedMessages, _, err := provider.BeforeRun(t.Context(), turnMessages(3), agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := messageTexts(compactedMessages), []string{"u3", "a3"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected compacted messages: got %v want %v", got, want)
	}

	var state struct {
		MessageGroups []*compaction.MessageGroup `json:"messagegroups,omitempty"`
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if ok, err := restored.Get("compaction-test", &state); err != nil || !ok {
		t.Fatalf("expected persisted provider state, ok=%v err=%v", ok, err)
	}
	if len(state.MessageGroups) != 6 {
		t.Fatalf("expected persisted index to preserve all groups, got %d", len(state.MessageGroups))
	}
}

func TestNewProvider_CompactsWithoutSession(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{
			Trigger:                compaction.GroupsExceed(2),
			MinimumPreservedGroups: 2,
		},
	})

	compactedMessages, _, err := provider.BeforeRun(t.Context(), turnMessages(3))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := messageTexts(compactedMessages), []string{"u3", "a3"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected compacted messages: got %v want %v", got, want)
	}
}

func TestMessageIndex_UsesTokenCounterForTextContent(t *testing.T) {
	counter := tokenCounterFunc(func(text string) int { return len(text) + 10 })
	messages := []*message.Message{{
		Role: message.RoleAssistant,
		Contents: []message.Content{
			&message.TextContent{Text: "abc"},
			&message.TextReasoningContent{Text: "xy", ProtectedData: "z"},
		},
	}}
	index := compaction.CreateMessageIndex(messages, counter)

	if got, want := index.Groups[0].TokenCount, 36; got != want {
		t.Fatalf("unexpected token count: got %d want %d", got, want)
	}
}

func isSummaryMessage(msg *message.Message) bool {
	index := compaction.CreateMessageIndex([]*message.Message{msg}, nil)
	return len(index.Groups) == 1 && index.Groups[0].Kind == compaction.GroupKindSummary
}

type tokenCounterFunc func(string) int

func (f tokenCounterFunc) CountTokens(text string) int { return f(text) }

func turnMessages(turns int) []*message.Message {
	messages := make([]*message.Message, 0, turns*2)
	for i := 1; i <= turns; i++ {
		messages = append(messages,
			textMessage(message.RoleUser, "u"+string(rune('0'+i))),
			textMessage(message.RoleAssistant, "a"+string(rune('0'+i))),
		)
	}
	return messages
}

func textMessage(role message.Role, text string) *message.Message {
	return &message.Message{
		Role: role,
		Contents: []message.Content{
			&message.TextContent{Text: text},
		},
	}
}

func messageTexts(messages []*message.Message) []string {
	texts := make([]string, len(messages))
	for i, msg := range messages {
		texts[i] = msg.String()
	}
	return texts
}
