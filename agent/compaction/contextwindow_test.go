// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/message"
)

// charCounter counts tokens as the number of characters in the text.
type charCounter struct{}

func (charCounter) CountTokens(text string) int { return len(text) }

// buildToolCallMessages creates a sequence of user + tool-call + tool-result + assistant messages.
// Each turn produces a group of 3 messages (assistant call, tool result, and assistant reply),
// preceded by a user message.
func buildToolCallMessages(n int) []*message.Message {
	msgs := make([]*message.Message, 0, n*4)
	for i := 0; i < n; i++ {
		callID := "call-" + string(rune('a'+i))
		msgs = append(msgs,
			textMessage(message.RoleUser, "u"),
			&message.Message{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.FunctionCallContent{CallID: callID, Name: "fn"}},
			},
			&message.Message{
				Role:     message.RoleTool,
				Contents: []message.Content{&message.FunctionResultContent{CallID: callID, Result: "r"}},
			},
			textMessage(message.RoleAssistant, "a"),
		)
	}
	return msgs
}

func TestContextWindowStrategy_ZeroValueErrors(t *testing.T) {
	s := &compaction.ContextWindowStrategy{}
	index := compaction.CreateMessageIndex(buildToolCallMessages(5), charCounter{})
	_, err := s.Compact(t.Context(), index)
	if err == nil {
		t.Fatal("expected error for zero MaxContextWindowTokens")
	}
}

func TestContextWindowStrategy_InvalidMaxOutputTokens(t *testing.T) {
	cases := []struct {
		name      string
		maxCtx    int
		maxOutput int
	}{
		{"negative", 1000, -1},
		{"equal", 1000, 1000},
		{"greater", 1000, 1500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &compaction.ContextWindowStrategy{
				MaxContextWindowTokens: tc.maxCtx,
				MaxOutputTokens:        tc.maxOutput,
			}
			index := compaction.CreateMessageIndex(buildToolCallMessages(3), charCounter{})
			_, err := s.Compact(t.Context(), index)
			if err == nil {
				t.Fatalf("expected error for MaxOutputTokens=%d with MaxContextWindowTokens=%d", tc.maxOutput, tc.maxCtx)
			}
		})
	}
}

func TestContextWindowStrategy_InvalidThresholds(t *testing.T) {
	cases := []struct {
		name       string
		evict      float64
		trunc      float64
		expectsErr bool
	}{
		{"eviction_zero", 0, 0.8, false}, // zero means "use default"
		{"eviction_negative", -0.1, 0.8, true},
		{"eviction_above_one", 1.1, 1.1, true},
		{"truncation_negative", 0.5, -0.1, true},
		{"truncation_above_one", 0.5, 1.1, true},
		{"truncation_below_eviction", 0.8, 0.5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &compaction.ContextWindowStrategy{
				MaxContextWindowTokens: 1000,
				MaxOutputTokens:        100,
				ToolEvictionThreshold:  tc.evict,
				TruncationThreshold:    tc.trunc,
			}
			index := compaction.CreateMessageIndex(buildToolCallMessages(3), charCounter{})
			_, err := s.Compact(t.Context(), index)
			if tc.expectsErr && err == nil {
				t.Fatalf("expected error for eviction=%g truncation=%g", tc.evict, tc.trunc)
			}
			if !tc.expectsErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestContextWindowStrategy_NoCompactionWhenUnderBudget(t *testing.T) {
	// Build messages with a tiny token footprint; budget is set very large.
	msgs := buildToolCallMessages(3)
	index := compaction.CreateMessageIndex(msgs, charCounter{})
	initialCount := index.IncludedGroupCount()

	s := &compaction.ContextWindowStrategy{
		MaxContextWindowTokens: 1_000_000,
		MaxOutputTokens:        100_000,
	}
	compacted, err := s.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if compacted {
		t.Fatal("expected no compaction when well under budget")
	}
	if got := index.IncludedGroupCount(); got != initialCount {
		t.Fatalf("group count changed: got %d want %d", got, initialCount)
	}
}

func TestContextWindowStrategy_EvictsToolResultsBeforeTruncating(t *testing.T) {
	// Each "u" and "a" message is 1 char each; tool call/result are 1 char too.
	// With charCounter each token = 1 character.
	// Build enough messages so the index exceeds the tool-eviction threshold.
	msgs := buildToolCallMessages(5)
	msgs = append(msgs, textMessage(message.RoleUser, "final"))

	index := compaction.CreateMessageIndex(msgs, charCounter{})
	totalTokens := index.IncludedTokenCount()

	// Set budget such that tool-eviction fires (threshold = 0.1 so even a small count triggers).
	s := &compaction.ContextWindowStrategy{
		MaxContextWindowTokens: totalTokens + 1,
		MaxOutputTokens:        0,
		ToolEvictionThreshold:  0.01, // always fires
		TruncationThreshold:    0.99, // never fires on this dataset
	}
	compacted, err := s.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	// At least one tool-call group should have been collapsed into a summary.
	included := index.IncludedMessages()
	hasSummary := false
	for _, m := range included {
		summaryIndex := compaction.CreateMessageIndex([]*message.Message{m}, nil)
		if len(summaryIndex.Groups) == 1 && summaryIndex.Groups[0].Kind == compaction.GroupKindSummary {
			hasSummary = true
			break
		}
	}
	if !hasSummary {
		t.Fatal("expected at least one tool-call group to be collapsed into a summary")
	}
}

func TestContextWindowStrategy_TruncatesWhenOverTruncationThreshold(t *testing.T) {
	// Use enough plain text turns (no tool calls) so tool eviction does nothing,
	// but truncation fires.
	msgs := []*message.Message{
		textMessage(message.RoleSystem, "system"),
	}
	for i := 0; i < 8; i++ {
		msgs = append(msgs,
			textMessage(message.RoleUser, "uuuu"),
			textMessage(message.RoleAssistant, "aaaa"),
		)
	}

	index := compaction.CreateMessageIndex(msgs, charCounter{})
	totalTokens := index.IncludedTokenCount()

	// tool-eviction threshold set equal to truncation threshold (no tool calls anyway),
	// truncation threshold set low so it fires.
	s := &compaction.ContextWindowStrategy{
		MaxContextWindowTokens: totalTokens + 1,
		MaxOutputTokens:        0,
		ToolEvictionThreshold:  0.1,
		TruncationThreshold:    0.1, // fires immediately; eviction threshold must be <= truncation
	}
	compacted, err := s.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	// System message must be preserved.
	included := index.IncludedMessages()
	if !slices.ContainsFunc(included, func(m *message.Message) bool {
		return m.Role == message.RoleSystem
	}) {
		t.Fatal("system message should be preserved after truncation")
	}

	// Some user/assistant groups should have been removed.
	if got := index.IncludedGroupCount(); got >= index.TotalGroupCount() {
		t.Fatal("expected some groups to be excluded by truncation")
	}
}

func TestContextWindowStrategy_DefaultThresholdsUsedWhenZero(t *testing.T) {
	// Verify that zero thresholds fall back to the documented defaults.
	// We do this by comparing the result of a zero-threshold strategy with
	// an explicitly configured one using the same defaults.
	msgs := buildToolCallMessages(6)
	msgs = append(msgs, textMessage(message.RoleUser, "latest"))

	makeIndex := func() *compaction.MessageIndex {
		return compaction.CreateMessageIndex(msgs, charCounter{})
	}
	index1 := makeIndex()
	index2 := makeIndex()

	totalTokens := index1.IncludedTokenCount()
	budget := totalTokens + 1 // budget just above current total so thresholds can fire

	zeroS := &compaction.ContextWindowStrategy{
		MaxContextWindowTokens: budget,
		MaxOutputTokens:        0,
		// ToolEvictionThreshold and TruncationThreshold are zero → defaults used
	}
	explicit := &compaction.ContextWindowStrategy{
		MaxContextWindowTokens: budget,
		MaxOutputTokens:        0,
		ToolEvictionThreshold:  0.5, // default tool eviction threshold
		TruncationThreshold:    0.8, // default truncation threshold
	}

	_, err := zeroS.Compact(t.Context(), index1)
	if err != nil {
		t.Fatalf("zero-threshold strategy error: %v", err)
	}
	_, err = explicit.Compact(t.Context(), index2)
	if err != nil {
		t.Fatalf("explicit-threshold strategy error: %v", err)
	}

	got1 := index1.IncludedGroupCount()
	got2 := index2.IncludedGroupCount()
	if got1 != got2 {
		t.Fatalf("zero-threshold (%d groups) != explicit (%d groups)", got1, got2)
	}
}
