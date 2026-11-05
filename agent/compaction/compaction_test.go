// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func invokeProvider(provider agent.ContextProvider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

type strategyFunc func(context.Context, *compaction.MessageIndex) (bool, error)

func (f strategyFunc) Compact(ctx context.Context, index *compaction.MessageIndex) (bool, error) {
	return f(ctx, index)
}

func TestSummarizerFunc_NilReturnsError(t *testing.T) {
	var summarizer compaction.SummarizerFunc
	_, err := summarizer.Summarize(t.Context(), nil)
	if err == nil || err.Error() != "compaction: summarizer function is nil" {
		t.Fatalf("Summarize() error = %v, want nil-function error", err)
	}
}

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

	wantKinds := []compaction.GroupKind{
		compaction.GroupKindSystem,
		compaction.GroupKindUser,
		compaction.GroupKindToolCall,
		compaction.GroupKindAssistantText,
	}
	for _, inputLength := range []int{len(messages), len(messages) - 1} {
		index := compaction.CreateMessageIndex(messages[:inputLength], nil)
		gotKinds := make([]compaction.GroupKind, len(index.Groups))
		for i, group := range index.Groups {
			gotKinds[i] = group.Kind
		}
		expectedKinds := wantKinds
		if inputLength == len(messages)-1 {
			expectedKinds = wantKinds[:3]
		}
		if !slices.Equal(gotKinds, expectedKinds) {
			t.Fatalf("unexpected group kinds for %d messages: got %v want %v", inputLength, gotKinds, expectedKinds)
		}
		if got := index.Groups[2].MessageCount; got != 3 {
			t.Fatalf("expected tool-call group to contain reasoning, call, and result messages, got %d", got)
		}
		if index.Groups[2].TurnIndex == nil || *index.Groups[2].TurnIndex != 1 {
			t.Fatalf("expected tool-call group to belong to turn 1")
		}
		if index.Groups[0].TurnIndex != nil {
			t.Errorf("system group turn = %v, want nil", *index.Groups[0].TurnIndex)
		}
		if index.Groups[1].TurnIndex == nil || *index.Groups[1].TurnIndex != 1 {
			t.Error("expected user group to belong to turn 1")
		}
	}
}

func TestMessageIndex_SummaryPropertyValueParsing(t *testing.T) {
	rawFalse := json.RawMessage(`false`)
	rawTrue := json.RawMessage(`true`)
	tests := []struct {
		name     string
		value    any
		expected compaction.GroupKind
	}{
		{name: "missing", expected: compaction.GroupKindAssistantText},
		{name: "bool false", value: false, expected: compaction.GroupKindAssistantText},
		{name: "raw false", value: json.RawMessage(`false`), expected: compaction.GroupKindAssistantText},
		{name: "raw null", value: json.RawMessage(`null`), expected: compaction.GroupKindAssistantText},
		{name: "raw string", value: json.RawMessage(`"Unexpected string"`), expected: compaction.GroupKindAssistantText},
		{name: "bool true", value: true, expected: compaction.GroupKindSummary},
		{name: "raw true", value: json.RawMessage(`true`), expected: compaction.GroupKindSummary},
		{name: "ptr raw nil", value: (*json.RawMessage)(nil), expected: compaction.GroupKindAssistantText},
		{name: "ptr raw false", value: &rawFalse, expected: compaction.GroupKindAssistantText},
		{name: "ptr raw true", value: &rawTrue, expected: compaction.GroupKindSummary},
		{name: "bytes false", value: []byte(`false`), expected: compaction.GroupKindAssistantText},
		{name: "bytes null", value: []byte(`null`), expected: compaction.GroupKindAssistantText},
		{name: "bytes true", value: []byte(`true`), expected: compaction.GroupKindSummary},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := textMessage(message.RoleAssistant, "Hello")
			if tt.name != "missing" {
				msg.AdditionalProperties = map[string]any{
					"_is_summary": tt.value,
				}
			}

			index := compaction.CreateMessageIndex([]*message.Message{msg}, nil)

			if len(index.Groups) != 1 {
				t.Fatalf("expected one group, got %d", len(index.Groups))
			}
			if got := index.Groups[0].Kind; got != tt.expected {
				t.Fatalf("group kind = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTruncationStrategy_ExcludesOldestGroups(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(2), nil)
	strategy := &compaction.TruncationStrategy{
		Trigger:                compaction.GroupsExceed(2),
		MinimumPreservedGroups: new(1),
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}
	if got := index.IncludedGroupCount(); got != 2 {
		t.Fatalf("expected included group count 2, got %d", got)
	}
	for i, want := range []bool{true, true, false, false} {
		if got := index.Groups[i].IsExcluded; got != want {
			t.Fatalf("group %d exclusion: got %v want %v", i, got, want)
		}
	}

	got := messageTexts(index.IncludedMessages())
	want := []string{"u2", "a2"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
}

func TestTruncationStrategy_PreservesRawJSONBelowTokenLimit(t *testing.T) {
	const payload = `{"order_id":"ORD-1042","status":"shipped"}`
	for _, tt := range []struct {
		name   string
		result any
	}{
		{name: "raw JSON", result: json.RawMessage(payload)},
		{name: "string", result: payload},
	} {
		t.Run(tt.name, func(t *testing.T) {
			messages := []*message.Message{
				textMessage(message.RoleUser, "lookup"),
				functionCallMessage("call-1", "lookup_order"),
				functionResultMessage("call-1", tt.result),
			}
			index := compaction.CreateMessageIndex(messages, nil)
			if got, want := index.TotalByteCount(), 72; got != want {
				t.Errorf("TotalByteCount = %d, want %d", got, want)
			}
			strategy := &compaction.TruncationStrategy{
				Trigger:                compaction.TokensExceed(25),
				MinimumPreservedGroups: new(1),
			}
			if compacted, err := strategy.Compact(t.Context(), index); err != nil || compacted {
				t.Fatalf("Compact = %t, %v; want no compaction below the token limit", compacted, err)
			}
			if got, want := len(index.IncludedMessages()), len(messages); got != want {
				t.Errorf("retained %d messages, want %d", got, want)
			}
		})
	}
}

func TestTruncationStrategy_PreservesDataContentBelowTokenLimit(t *testing.T) {
	for _, tt := range []struct {
		name string
		size int
	}{
		{name: "empty"},
		{name: "no padding", size: 3000},
		{name: "two padding characters", size: 3001},
		{name: "one padding character", size: 3002},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := message.NewDataContent(bytes.Repeat([]byte("x"), tt.size), "text/plain")
			if err != nil {
				t.Fatal(err)
			}
			data.Name = "notes.txt"
			messages := []*message.Message{
				{Role: message.RoleUser, Contents: []message.Content{data}},
				textMessage(message.RoleAssistant, "Received."),
				textMessage(message.RoleUser, "Summarize it."),
			}
			index := compaction.CreateMessageIndex(messages, nil)
			want := tt.size + len(data.MediaType) + len(data.Name)
			want += len(messages[1].String()) + len(messages[2].String())
			if got := index.TotalByteCount(); got != want {
				t.Errorf("TotalByteCount = %d, want %d", got, want)
			}
			strategy := &compaction.TruncationStrategy{
				Trigger:                compaction.TokensExceed(900),
				MinimumPreservedGroups: new(1),
			}
			if compacted, err := strategy.Compact(t.Context(), index); err != nil || compacted {
				t.Fatalf("Compact = %t, %v; want no compaction below the token limit", compacted, err)
			}
			if !slices.Equal(index.IncludedMessages(), messages) {
				t.Error("compaction did not preserve the original messages")
			}
		})
	}
}

func TestTruncationStrategy_SkipsPreExcludedAndSystemGroups(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "system"),
		textMessage(message.RoleUser, "u1"),
		textMessage(message.RoleAssistant, "a1"),
		textMessage(message.RoleUser, "u2"),
	}, nil)
	index.Groups[1].IsExcluded = true
	strategy := &compaction.TruncationStrategy{
		MinimumPreservedGroups: new(1),
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}

	if index.Groups[0].IsExcluded {
		t.Fatal("expected system group to be preserved")
	}
	if !index.Groups[1].IsExcluded {
		t.Fatal("expected pre-excluded group to remain excluded")
	}
	if !index.Groups[2].IsExcluded {
		t.Fatal("expected oldest removable group to be excluded")
	}
	if index.Groups[3].IsExcluded {
		t.Fatal("expected minimum preserved group to remain included")
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
		Trigger:               compaction.TurnsExceed(2),
		MinimumPreservedTurns: new(1),
	}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}
	for i, want := range []bool{true, true, false, false, false, false} {
		if got := index.Groups[i].IsExcluded; got != want {
			t.Fatalf("group %d exclusion: got %v want %v", i, got, want)
		}
	}

	got := messageTexts(index.IncludedMessages())
	want := []string{"u2", "a2", "u3", "a3"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
}

func TestSlidingWindowStrategy_PreservesTurnZeroGroups(t *testing.T) {
	for _, tt := range []struct {
		name    string
		trigger compaction.Trigger
	}{
		{name: "zero floor with always trigger", trigger: compaction.Always()},
		{name: "zero floor with turn threshold trigger", trigger: compaction.TurnsExceed(1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			index := compaction.CreateMessageIndex([]*message.Message{
				textMessage(message.RoleAssistant, "preface"),
				textMessage(message.RoleUser, "u1"),
				textMessage(message.RoleAssistant, "a1"),
				textMessage(message.RoleUser, "u2"),
				textMessage(message.RoleAssistant, "a2"),
			}, nil)
			strategy := &compaction.SlidingWindowStrategy{
				Trigger:               tt.trigger,
				Target:                compaction.Never(),
				MinimumPreservedTurns: new(0),
			}

			compacted, err := strategy.Compact(t.Context(), index)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !compacted {
				t.Fatal("expected compaction")
			}
			wantExcluded := []bool{false, true, true, true, true}
			if got := len(index.Groups); got != len(wantExcluded) {
				t.Fatalf("unexpected group count: got %d want %d", got, len(wantExcluded))
			}
			for i, want := range wantExcluded {
				if got := index.Groups[i].IsExcluded; got != want {
					t.Fatalf("group %d exclusion: got %v want %v", i, got, want)
				}
			}
			if got, want := messageTexts(index.IncludedMessages()), []string{"preface"}; !slices.Equal(got, want) {
				t.Fatalf("unexpected included messages: got %v want %v", got, want)
			}
		})
	}

	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleAssistant, "preface"),
		textMessage(message.RoleUser, "u1"),
		textMessage(message.RoleAssistant, "a1"),
		textMessage(message.RoleUser, "u2"),
		textMessage(message.RoleAssistant, "a2"),
	}, nil)
	strategy := &compaction.SlidingWindowStrategy{
		Trigger:               compaction.TurnsExceed(1),
		MinimumPreservedTurns: new(1),
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

func TestTruncationStrategy_ExplicitZeroPreservesNone(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "system"),
		textMessage(message.RoleAssistant, "g1"),
		textMessage(message.RoleAssistant, "g2"),
		textMessage(message.RoleAssistant, "g3"),
		textMessage(message.RoleAssistant, "g4"),
		textMessage(message.RoleAssistant, "g5"),
	}, nil)
	strategy := &compaction.TruncationStrategy{MinimumPreservedGroups: new(0)}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction with an explicit zero floor")
	}
	if got := index.IncludedNonSystemGroupCount(); got != 0 {
		t.Fatalf("expected all removable non-system groups excluded, got %d preserved", got)
	}
	if index.Groups[0].IsExcluded {
		t.Fatal("expected system group to be preserved")
	}
}

func TestSlidingWindowStrategy_NegativeMinimumClampsToZero(t *testing.T) {
	index := compaction.CreateMessageIndex(turnMessages(3), nil)
	strategy := &compaction.SlidingWindowStrategy{MinimumPreservedTurns: new(-5)}

	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction when the floor clamps to zero")
	}
	if got := index.IncludedTurnCount(); got != 0 {
		t.Fatalf("expected a negative floor to clamp to zero and exclude all turns, got %d turns preserved", got)
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
		MinimumPreservedGroups: new(2),
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

func TestDefaultToolCallFormatter_ResultTypes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result any
		want   string
	}{
		{name: "raw JSON object", result: json.RawMessage(`{"status":"shipped"}`), want: "[Tool Calls]\nlookup:\n  - {\"status\":\"shipped\"}"},
		{name: "raw JSON array", result: json.RawMessage(`["first","second"]`), want: "[Tool Calls]\nlookup:\n  - [\"first\",\"second\"]"},
		{name: "raw JSON null", result: json.RawMessage(`null`), want: "[Tool Calls]\nlookup:\n  - null"},
		{name: "empty raw JSON", result: json.RawMessage{}, want: "[Tool Calls]\nlookup:"},
		{name: "nil raw JSON", result: json.RawMessage(nil), want: "[Tool Calls]\nlookup:"},
		{name: "string", result: "shipped", want: "[Tool Calls]\nlookup:\n  - shipped"},
		{name: "number", result: 42, want: "[Tool Calls]\nlookup:\n  - 42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &compaction.MessageGroup{
				Messages: []*message.Message{
					{
						Role: message.RoleAssistant,
						Contents: []message.Content{
							&message.FunctionCallContent{CallID: "call-1", Name: "lookup"},
						},
					},
					{
						Role: message.RoleTool,
						Contents: []message.Content{
							&message.FunctionResultContent{CallID: "call-1", Result: tc.result},
						},
					},
				},
			}
			if got := compaction.DefaultToolCallFormatter(group); got != tc.want {
				t.Fatalf("formatter output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDefaultToolCallFormatter_DedupsRepeatedNamesWithEmptyResults guards the
// tool-name deduplication when repeated calls to the same tool produce empty
// results. The name must still be listed exactly once, matching the behavior
// for non-empty results.
func TestDefaultToolCallFormatter_DedupsRepeatedNamesWithEmptyResults(t *testing.T) {
	group := &compaction.MessageGroup{
		Messages: []*message.Message{
			{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "c1", Name: "notify"},
					&message.FunctionCallContent{CallID: "c2", Name: "notify"},
				},
			},
			{
				Role: message.RoleTool,
				Contents: []message.Content{
					&message.FunctionResultContent{CallID: "c1", Result: ""},
					&message.FunctionResultContent{CallID: "c2", Result: ""},
				},
			},
		},
	}

	got := compaction.DefaultToolCallFormatter(group)
	want := "[Tool Calls]\nnotify:"
	if got != want {
		t.Fatalf("formatter output = %q, want %q", got, want)
	}
}

func TestToolResultStrategy_ZeroValueUsesDefaults(t *testing.T) {
	messages := make([]*message.Message, 0, 20)
	for i := range 9 {
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
	index := compaction.CreateMessageIndex(turnMessages(2), nil)
	var summarized []string
	summarizer := compaction.SummarizerFunc(func(_ context.Context, messages []*message.Message) (string, error) {
		summarized = messageTexts(messages)
		return "older context", nil
	})
	strategy := &compaction.SummarizationStrategy{
		Trigger:                compaction.GroupsExceed(0),
		Summarizer:             summarizer,
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

	if want := []string{"summarize", "u1", "a1"}; !slices.Equal(summarized, want) {
		t.Fatalf("unexpected summarizer input: got %v want %v", summarized, want)
	}
	got := messageTexts(index.IncludedMessages())
	want := []string{"[Summary]\nolder context", "u2", "a2"}
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
	called := false
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.GroupsExceed(0),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			called = true
			return "", expected
		}),
		MinimumPreservedGroups: new(1),
	}

	originalIncluded := index.IncludedMessages()
	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected failing summarizer to be called")
	}
	if compacted {
		t.Fatal("expected no compaction when summarizer fails")
	}
	if got := index.IncludedMessages(); !slices.Equal(got, originalIncluded) {
		t.Fatalf("expected original included message references in order, got %#v want %#v", got, originalIncluded)
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
		MinimumPreservedGroups: new(1),
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

func TestNewContextProvider_PanicsWithBlankStateKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config compaction.ContextProviderConfig
	}{
		{name: "explicit state key", config: compaction.ContextProviderConfig{StateKey: " "}},
		{name: "default state key from source ID", config: compaction.ContextProviderConfig{SourceID: "\t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected blank compaction state key to panic at construction")
				}
			}()
			tc.config.Strategy = new(compaction.TruncationStrategy)
			compaction.NewContextProvider(tc.config)
		})
	}
}

func TestNewProvider_DefaultStateIsIsolatedByStrategyType(t *testing.T) {
	session := agenttest.CreateSession()
	truncation := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{
			Trigger: compaction.GroupsExceed(2), MinimumPreservedGroups: new(2),
		},
	})
	result, _, err := invokeProvider(truncation, t.Context(), turnMessages(3), agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messageTexts(result), []string{"u3", "a3"}; !slices.Equal(got, want) {
		t.Fatalf("initial truncation = %v, want %v", got, want)
	}

	for _, restored := range []bool{false, true} {
		t.Run(fmt.Sprintf("restored=%t", restored), func(t *testing.T) {
			if restored {
				data, err := json.Marshal(session)
				if err != nil {
					t.Fatal(err)
				}
				session = agenttest.CreateSession()
				if err := json.Unmarshal(data, session); err != nil {
					t.Fatal(err)
				}
			}
			// A different strategy must not inherit truncation's exclusions.
			window := compaction.NewContextProvider(compaction.ContextProviderConfig{
				Strategy: &compaction.SlidingWindowStrategy{Trigger: compaction.Never()},
			})
			result, _, err := invokeProvider(window, t.Context(), turnMessages(3), agent.WithSession(session))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := messageTexts(result), messageTexts(turnMessages(3)); !slices.Equal(got, want) {
				t.Errorf("different strategy inherited exclusions: got %v, want %v", got, want)
			}

			// Recreating the original strategy type must recover its saved state,
			// even when its new configuration would not trigger compaction.
			recreated := compaction.NewContextProvider(compaction.ContextProviderConfig{
				Strategy: &compaction.TruncationStrategy{Trigger: compaction.Never()},
			})
			result, _, err = invokeProvider(recreated, t.Context(), turnMessages(3), agent.WithSession(session))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := messageTexts(result), []string{"u3", "a3"}; !slices.Equal(got, want) {
				t.Errorf("recreated strategy lost exclusions: got %v, want %v", got, want)
			}
		})
	}
}

func TestNewProvider_CustomStrategyStateSurvivesRecreation(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(strategyFunc) compaction.Strategy
	}{
		{"function value", func(f strategyFunc) compaction.Strategy { return f }},
		{"function pointer", func(f strategyFunc) compaction.Strategy { return &f }},
		{"unnamed struct", func(f strategyFunc) compaction.Strategy {
			return struct{ compaction.Strategy }{f}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := agenttest.CreateSession()
			truncation := &compaction.TruncationStrategy{
				Trigger: compaction.GroupsExceed(2), MinimumPreservedGroups: new(2),
			}
			provider := compaction.NewContextProvider(compaction.ContextProviderConfig{Strategy: tc.wrap(truncation.Compact)})
			if _, _, err := invokeProvider(provider, t.Context(), turnMessages(3), agent.WithSession(session)); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(session)
			if err != nil {
				t.Fatal(err)
			}
			restored := agenttest.CreateSession()
			if err := json.Unmarshal(data, restored); err != nil {
				t.Fatal(err)
			}
			provider = compaction.NewContextProvider(compaction.ContextProviderConfig{
				Strategy: tc.wrap(func(context.Context, *compaction.MessageIndex) (bool, error) { return false, nil }),
			})
			result, _, err := invokeProvider(provider, t.Context(), turnMessages(3), agent.WithSession(restored))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := messageTexts(result), []string{"u3", "a3"}; !slices.Equal(got, want) {
				t.Fatalf("recreated custom strategy lost exclusions: got %v, want %v", got, want)
			}
		})
	}
}

func TestNewProvider_ExplicitStateKeyRetainsLegacyState(t *testing.T) {
	session := agenttest.CreateSession()
	// SourceID still selects the same storage key as the previous default.
	legacy := compaction.NewContextProvider(compaction.ContextProviderConfig{
		SourceID: "CompactionProvider",
		Strategy: &compaction.TruncationStrategy{
			Trigger: compaction.GroupsExceed(2), MinimumPreservedGroups: new(2),
		},
	})
	if _, _, err := invokeProvider(legacy, t.Context(), turnMessages(3), agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	restored := agenttest.CreateSession()
	if err := json.Unmarshal(data, restored); err != nil {
		t.Fatal(err)
	}
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		StateKey: "CompactionProvider", SourceID: "new-attribution",
		Strategy: &compaction.TruncationStrategy{Trigger: compaction.Never()},
	})
	result, _, err := invokeProvider(provider, t.Context(), turnMessages(3), agent.WithSession(restored))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messageTexts(result), []string{"u3", "a3"}; !slices.Equal(got, want) {
		t.Fatalf("legacy state was not retained: got %v, want %v", got, want)
	}
}

func TestNewProvider_CompactsAndPersistsIndex(t *testing.T) {
	session := agenttest.CreateSession()
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{
			Trigger:                compaction.GroupsExceed(2),
			MinimumPreservedGroups: new(2),
		},
		SourceID: "compaction-test",
	})

	compactedMessages, _, err := invokeProvider(provider, t.Context(), turnMessages(3), agent.WithSession(session))
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
	restored := agenttest.CreateSession()
	if err := json.Unmarshal(data, restored); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if ok, err := restored.Get("compaction-test", &state); err != nil || !ok {
		t.Fatalf("expected persisted provider state, ok=%v err=%v", ok, err)
	}
	if len(state.MessageGroups) != 6 {
		t.Fatalf("expected persisted index to preserve all groups, got %d", len(state.MessageGroups))
	}
}

func TestNewProvider_ConcurrentInvokingPreservesState(t *testing.T) {
	session := agenttest.CreateSession()
	const invocations = 64
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		SourceID: "concurrent-compaction",
		Strategy: strategyFunc(func(_ context.Context, index *compaction.MessageIndex) (bool, error) {
			if len(index.Groups) != 1 {
				return false, fmt.Errorf("groups = %d, want one user group", len(index.Groups))
			}
			previous := 0
			if index.Groups[0].ExcludeReason != "" {
				var err error
				previous, err = strconv.Atoi(index.Groups[0].ExcludeReason)
				if err != nil {
					return false, err
				}
			}
			runtime.Gosched()
			index.Groups[0].ExcludeReason = strconv.Itoa(previous + 1)
			return true, nil
		}),
	})

	start := make(chan struct{})
	errs := make(chan error, invocations)
	var wg sync.WaitGroup
	wg.Add(invocations)
	for range invocations {
		go func() {
			defer wg.Done()
			<-start
			_, _, err := invokeProvider(provider, context.Background(), []*message.Message{textMessage(message.RoleUser, "request")}, agent.WithSession(session))
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	var state struct {
		MessageGroups []*compaction.MessageGroup `json:"messagegroups"`
	}
	if ok, err := restored.Get("concurrent-compaction", &state); err != nil || !ok || len(state.MessageGroups) != 1 {
		t.Fatalf("stored groups = %v, found = %v, err = %v; want one group", state.MessageGroups, ok, err)
	}
	if got := state.MessageGroups[0].ExcludeReason; got != strconv.Itoa(invocations) {
		t.Fatalf("compaction updates = %q, want %d", got, invocations)
	}
}

func TestNewProvider_SourceStampsGeneratedMessages(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.SummarizationStrategy{
			Trigger:                compaction.GroupsExceed(2),
			Summarizer:             compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) { return "older context", nil }),
			MinimumPreservedGroups: new(2),
		},
		SourceID: "compaction-test",
	})

	compactedMessages, _, err := invokeProvider(provider, t.Context(), turnMessages(3), agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := messageTexts(compactedMessages), []string{"[Summary]\nolder context", "u3", "a3"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected compacted messages: got %v want %v", got, want)
	}
	if got, want := compactedMessages[0].Source, (message.Source{Type: agent.SourceTypeHistoryProvider, ID: "compaction-test"}); got != want {
		t.Fatalf("summary source = %#v, want %#v", got, want)
	}
	if compactedMessages[1].Source != (message.Source{}) || compactedMessages[2].Source != (message.Source{}) {
		t.Fatalf("expected preserved messages to keep original sources, got %#v and %#v", compactedMessages[1].Source, compactedMessages[2].Source)
	}
}

func TestNewProvider_DoesNotStoreGeneratedSummaryInHistory(t *testing.T) {
	options := []agent.Option{agent.WithSession(agenttest.CreateSession())}
	history := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{
		StateInitializer: func(*agent.Session) []*message.Message { return turnMessages(1) },
	})
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.SummarizationStrategy{
			Summarizer:             compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) { return "older context", nil }),
			MinimumPreservedGroups: new(1),
		},
	})

	messages, err := history.Invoking(t.Context(), agent.InvokingContext{
		Messages: []*message.Message{textMessage(message.RoleUser, "u2")},
		Options:  options,
	})
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	messages, _, err = invokeProvider(provider, t.Context(), messages, options...)
	if err != nil {
		t.Fatalf("compact history: %v", err)
	}
	if got, want := messageTexts(messages), []string{"[Summary]\nolder context", "u2"}; !slices.Equal(got, want) {
		t.Fatalf("model request = %v, want %v", got, want)
	}
	if err := history.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages:  messages,
		ResponseMessages: []*message.Message{textMessage(message.RoleAssistant, "a2")},
		Options:          options,
	}); err != nil {
		t.Fatalf("store history: %v", err)
	}
	stored, err := history.Invoking(t.Context(), agent.InvokingContext{Options: options})
	if err != nil {
		t.Fatalf("reload history: %v", err)
	}
	if got, want := messageTexts(stored), []string{"u1", "a1", "u2", "a2"}; !slices.Equal(got, want) {
		t.Errorf("stored history = %v, want %v", got, want)
	}
}

func TestNewProvider_SourceStampingDoesNotMutateGeneratedMessage(t *testing.T) {
	generated := textMessage(message.RoleAssistant, "generated")
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: strategyFunc(func(_ context.Context, index *compaction.MessageIndex) (bool, error) {
			index.AddGroup(compaction.GroupKindSummary, []*message.Message{generated}, nil)
			return true, nil
		}),
		SourceID: "compaction-test",
	})

	input := textMessage(message.RoleUser, "input")
	compactedMessages, _, err := invokeProvider(provider, t.Context(), []*message.Message{input}, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(compactedMessages) != 2 || compactedMessages[0] != input {
		t.Fatalf("unexpected compacted messages: %#v", compactedMessages)
	}
	if compactedMessages[1] == generated {
		t.Fatal("expected generated message to be cloned before source attribution")
	}
	if generated.Source != (message.Source{}) {
		t.Fatalf("expected generated message source to remain unchanged, got %#v", generated.Source)
	}
}

func TestNewProvider_RequiresSessionForCompaction(t *testing.T) {
	for _, tc := range []struct {
		name          string
		options       []agent.Option
		wantCompacted bool
	}{
		{name: "omitted session"},
		{name: "nil session", options: []agent.Option{agent.WithSession(nil)}},
		{name: "local session", options: []agent.Option{agent.WithSession(agenttest.CreateSession())}, wantCompacted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			strategy := &compaction.TruncationStrategy{
				Trigger:                compaction.GroupsExceed(2),
				MinimumPreservedGroups: new(2),
			}
			called := false
			provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
				Strategy: strategyFunc(func(ctx context.Context, index *compaction.MessageIndex) (bool, error) {
					called = true
					return strategy.Compact(ctx, index)
				}),
			})
			messages := turnMessages(3)
			options := append(tc.options, agent.WithInstructions("Be helpful"))
			result, resultOptions, err := invokeProvider(provider, t.Context(), messages, options...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if called != tc.wantCompacted {
				t.Errorf("strategy called = %v, want %v", called, tc.wantCompacted)
			}
			want := messages
			if tc.wantCompacted {
				want = messages[len(messages)-2:]
			} else if len(result) == 0 || &result[0] != &messages[0] {
				t.Error("expected the original message slice without a session")
			}
			if !slices.Equal(result, want) {
				t.Errorf("messages = %v, want %v", messageTexts(result), messageTexts(want))
			}
			if len(resultOptions) != len(options) || &resultOptions[0] != &options[0] {
				t.Error("expected the original options")
			}
		})
	}
}

func TestNewProvider_MiddlewarePassesThroughWithoutSession(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{
			Trigger: compaction.GroupsExceed(2), MinimumPreservedGroups: new(2),
		},
	})
	messages := turnMessages(3)
	options := []agent.Option{agent.WithInstructions("Be helpful")}
	called := false
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, got []*message.Message, opts ...agent.Option) {
		called = true
		if !slices.Equal(got, messages) || len(got) == 0 || &got[0] != &messages[0] {
			t.Errorf("downstream messages = %v, want the original slice", messageTexts(got))
		}
		if len(opts) != len(options) || &opts[0] != &options[0] {
			t.Error("downstream options changed")
		}
	}).AddText("done").Build()}
	response, err := agent.ResponseStream(agent.ContextProviderMiddleware(provider).Run(runner.Run, t.Context(), messages, options...)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !called || response.String() != "done" {
		t.Fatal("expected the downstream response to pass through")
	}
}

func TestNewProvider_AgentRunCompactsWithImplicitSession(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{
			Trigger: compaction.GroupsExceed(2), MinimumPreservedGroups: new(2),
		},
	})
	called := false
	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, got []*message.Message, opts ...agent.Option) {
		called = true
		if session, _ := agent.GetOption(opts, agent.WithSession); session == nil {
			t.Error("expected Agent.Run to supply an implicit session")
		}
		if want := []string{"u3", "a3"}; !slices.Equal(messageTexts(got), want) {
			t.Errorf("downstream messages = %v, want %v", messageTexts(got), want)
		}
	}).AddText("done").Build()}
	a := agent.New(agent.ProviderConfig{Run: runner.Run}, agent.Config{ContextProviders: []agent.ContextProvider{provider}})
	response, err := a.Run(t.Context(), turnMessages(3)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !called || response.String() != "done" {
		t.Fatal("expected the downstream response to pass through")
	}
}

func TestNewProvider_SkipsServiceManagedSession(t *testing.T) {
	session := agenttest.CreateSession()
	session.SetServiceID("remote-conversation-id")
	called := false
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: strategyFunc(func(context.Context, *compaction.MessageIndex) (bool, error) {
			called = true
			return true, nil
		}),
	})
	messages := turnMessages(2)

	compactedMessages, _, err := invokeProvider(provider, t.Context(), messages, agent.WithSession(session))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("expected service-managed session to skip compaction")
	}
	if !slices.Equal(compactedMessages, messages) {
		t.Fatalf("expected original messages to be returned, got %v", messageTexts(compactedMessages))
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

func TestNewProvider_KeepsRetainedHistorySourceAcrossTurns(t *testing.T) {
	// A compaction provider generates only summary messages; genuine prior-turn
	// history returned from the persisted index must keep its original Source.
	// Across turns the index is rebuilt from persisted groups whose message
	// pointers differ from this turn's input, so identity-based attribution
	// wrongly stamps real history as context-provider generated.
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{Trigger: compaction.Never()},
		SourceID: "compaction-test",
	})
	session := agenttest.CreateSession()

	if _, _, err := invokeProvider(provider, t.Context(), []*message.Message{
		textMessage(message.RoleUser, "u1"),
		textMessage(message.RoleAssistant, "a1"),
	}, agent.WithSession(session)); err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	out, _, err := invokeProvider(provider, t.Context(), []*message.Message{
		textMessage(message.RoleUser, "u1"),
		textMessage(message.RoleAssistant, "a1"),
		textMessage(message.RoleUser, "u2"),
		textMessage(message.RoleAssistant, "a2"),
	}, agent.WithSession(session))
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	cp := message.Source{Type: agent.SourceTypeContextProvider, ID: "compaction-test"}
	for i, msg := range out {
		if msg.Source == cp {
			t.Errorf("message %d (%q) mislabeled as context-provider generated; genuine history must not be attributed to the provider", i, msg.String())
		}
	}
	// Retained prior-turn history restored from compaction state is attributed as
	// chat history (matching .NET), not context-provider generated.
	if len(out) < 2 || out[0].Source.Type != agent.SourceTypeHistoryProvider || out[1].Source.Type != agent.SourceTypeHistoryProvider {
		t.Errorf("retained history should be marked as chat history, got %#v and %#v", out[0].Source, out[1].Source)
	}
}

func compactIndex(t *testing.T, strategy compaction.Strategy, index *compaction.MessageIndex) bool {
	t.Helper()
	compacted, err := strategy.Compact(t.Context(), index)
	if err != nil {
		t.Fatal(err)
	}
	return compacted
}

func assertExclusionFlags(t *testing.T, index *compaction.MessageIndex, want ...bool) {
	t.Helper()
	got := make([]bool, len(index.Groups))
	for i, group := range index.Groups {
		got[i] = group.IsExcluded
	}
	if !slices.Equal(got, want) {
		t.Fatalf("group exclusions = %v, want %v", got, want)
	}
}

func TestTruncationStrategy_CompactAsyncAlwaysTriggerCompactsToPreserveRecentAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "First"),
		textMessage(message.RoleAssistant, "Response 1"),
		textMessage(message.RoleUser, "Second"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if got := index.IncludedGroupCount(); got != 1 {
		t.Fatalf("IncludedGroupCount = %d, want 1", got)
	}
}

func TestTruncationStrategy_CompactAsyncTriggerNotMetReturnsFalseAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.TokensExceed(1000), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Hello"),
		textMessage(message.RoleAssistant, "Hi!"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("unexpected compaction below the trigger threshold")
	}
	if got := index.IncludedGroupCount(); got != 2 {
		t.Fatalf("IncludedGroupCount = %d, want 2", got)
	}
}

func TestTruncationStrategy_CompactAsyncPreservesSystemMessagesAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "You are helpful."),
		textMessage(message.RoleUser, "First"),
		textMessage(message.RoleAssistant, "Response 1"),
		textMessage(message.RoleUser, "Second"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, false, true, true, false)
	if index.Groups[0].Kind != compaction.GroupKindSystem {
		t.Fatalf("first group kind = %v, want system", index.Groups[0].Kind)
	}
}

func TestTruncationStrategy_CompactAsyncPreservesToolCallGroupAtomicityAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		functionCallMessage("call1", "get_weather"),
		textMessage(message.RoleTool, "Sunny"),
		textMessage(message.RoleUser, "Thanks!"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, true, false)
	if group := index.Groups[0]; group.Kind != compaction.GroupKindToolCall || len(group.Messages) != 2 {
		t.Fatalf("excluded group kind/messages = %v/%d, want tool call/2", group.Kind, len(group.Messages))
	}
}

func TestTruncationStrategy_CompactAsyncSetsExcludeReasonAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Old"),
		textMessage(message.RoleUser, "New"),
	}, nil)
	compactIndex(t, strategy, index)
	if !strings.Contains(index.Groups[0].ExcludeReason, "TruncationStrategy") {
		t.Fatalf("ExcludeReason = %q, want strategy name", index.Groups[0].ExcludeReason)
	}
}

func TestTruncationStrategy_CompactAsyncSkipsAlreadyExcludedGroupsAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Already excluded"),
		textMessage(message.RoleUser, "Included 1"),
		textMessage(message.RoleUser, "Included 2"),
	}, nil)
	index.Groups[0].IsExcluded = true
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, true, true, false)
}

func TestTruncationStrategy_CompactAsyncMinimumPreservedKeepsMultipleAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(2)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, true, true, false, false)
}

func TestTruncationStrategy_CompactAsyncNothingToRemoveReturnsFalseAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(5)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Hello"),
		textMessage(message.RoleAssistant, "Hi!"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("compaction should respect the preserved-group floor")
	}
}

func TestTruncationStrategy_CompactAsyncCustomTargetStopsEarlyAsync(t *testing.T) {
	targetChecks := 0
	strategy := &compaction.TruncationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Target: func(*compaction.MessageIndex) bool { targetChecks++; return targetChecks >= 1 },
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleUser, "Q3"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, true, false, false, false)
}

func TestTruncationStrategy_CompactAsyncIncrementalStopsAtTargetAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.GroupsExceed(2), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
		textMessage(message.RoleUser, "Q3"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if got := index.IncludedGroupCount(); got != 2 {
		t.Fatalf("IncludedGroupCount = %d, want 2", got)
	}
}

func TestTruncationStrategy_CompactAsyncLoopExitsWhenMaxRemovableReachedAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(2), Target: compaction.Never(),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if got := index.IncludedGroupCount(); got != 2 {
		t.Fatalf("IncludedGroupCount = %d, want 2", got)
	}
	assertExclusionFlags(t, index, true, true, false, false)
}

func TestSlidingWindowStrategy_CompactAsyncBelowMaxTurnsReturnsFalseAsync(t *testing.T) {
	strategy := &compaction.SlidingWindowStrategy{Trigger: compaction.TurnsExceed(3)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("unexpected compaction below the turn threshold")
	}
}

func TestSlidingWindowStrategy_CompactAsyncPreservesSystemMessagesAsync(t *testing.T) {
	strategy := &compaction.SlidingWindowStrategy{Trigger: compaction.TurnsExceed(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "You are helpful."),
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, false, true, true, false)
}

func TestSlidingWindowStrategy_CompactAsyncPreservesToolCallGroupsInKeptTurnsAsync(t *testing.T) {
	strategy := &compaction.SlidingWindowStrategy{Trigger: compaction.TurnsExceed(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		functionCallMessage("call1", "search"),
		textMessage(message.RoleTool, "Results"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, true, true, false, false)
}

func TestSlidingWindowStrategy_CompactAsyncTriggerNotMetReturnsFalseAsync(t *testing.T) {
	strategy := &compaction.SlidingWindowStrategy{Trigger: compaction.TurnsExceed(99)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleUser, "Q3"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("unexpected compaction below the turn threshold")
	}
}

func TestSlidingWindowStrategy_CompactAsyncIncludedMessagesContainOnlyKeptTurnsAsync(t *testing.T) {
	strategy := &compaction.SlidingWindowStrategy{Trigger: compaction.TurnsExceed(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "System"),
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
	}, nil)
	compactIndex(t, strategy, index)
	if got, want := messageTexts(index.IncludedMessages()), []string{"System", "Q2", "A2"}; !slices.Equal(got, want) {
		t.Fatalf("included messages = %v, want %v", got, want)
	}
}

func TestSlidingWindowStrategy_CompactAsyncCustomTargetStopsExcludingEarlyAsync(t *testing.T) {
	removeCount := 0
	strategy := &compaction.SlidingWindowStrategy{
		Trigger: compaction.TurnsExceed(1), MinimumPreservedTurns: new(0),
		Target: func(*compaction.MessageIndex) bool { removeCount++; return removeCount >= 1 },
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
		textMessage(message.RoleUser, "Q3"),
		textMessage(message.RoleAssistant, "A3"),
		textMessage(message.RoleUser, "Q4"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, true, true, false, false, false, false, false)
}

func TestSlidingWindowStrategy_CompactAsyncMinimumPreservedStopsCompactionAsync(t *testing.T) {
	strategy := &compaction.SlidingWindowStrategy{
		Trigger: compaction.TurnsExceed(1), MinimumPreservedTurns: new(2), Target: compaction.Never(),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
		textMessage(message.RoleUser, "Q3"),
		textMessage(message.RoleAssistant, "A3"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if got := index.IncludedGroupCount(); got != 4 {
		t.Fatalf("IncludedGroupCount = %d, want 4", got)
	}
	assertExclusionFlags(t, index, true, true, false, false, false, false)
}

func TestSlidingWindowStrategy_CompactAsyncSkipsExcludedAndSystemGroupsInEnumerationAsync(t *testing.T) {
	strategy := &compaction.SlidingWindowStrategy{Trigger: compaction.TurnsExceed(1), MinimumPreservedTurns: new(0)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "System prompt"),
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	index.Groups[1].IsExcluded = true
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if index.Groups[0].IsExcluded {
		t.Fatal("system group was excluded")
	}
}

func TestSlidingWindowStrategy_CompactAsyncPreservesNullTurnIndexAsync(t *testing.T) {
	strategy := &compaction.SlidingWindowStrategy{
		Trigger: compaction.TurnsExceed(0), MinimumPreservedTurns: new(0), Target: compaction.Never(),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "You are helpful."),
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	assertExclusionFlags(t, index, false, true, true)
}

func TestToolResultStrategy_CompactAsyncTriggerNotMetReturnsFalseAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.TokensExceed(1000)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "What's the weather?"),
		functionCallMessage("call1", "get_weather"),
		textMessage(message.RoleTool, "Sunny"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("unexpected compaction below the token threshold")
	}
}

func TestToolResultStrategy_CompactAsyncCollapsesOldToolGroupsAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		functionCallMessage("call1", "get_weather"),
		textMessage(message.RoleTool, "Sunny and 72°F"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	want := []string{"Q1", "[Tool Calls]\nget_weather:\n  - Sunny and 72°F", "Q2"}
	if got := messageTexts(index.IncludedMessages()); !slices.Equal(got, want) {
		t.Fatalf("included messages = %q, want %q", got, want)
	}
}

func TestToolResultStrategy_CompactAsyncPreservesRecentToolGroupsAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(3)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		functionCallMessage("call1", "search"),
		textMessage(message.RoleTool, "Results"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("tool group in the preserved window should not be collapsed")
	}
}

func TestToolResultStrategy_CompactAsyncPreservesSystemMessagesAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "You are helpful."),
		textMessage(message.RoleUser, "Q1"),
		functionCallMessage("call1", "fn"),
		textMessage(message.RoleTool, "result"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	if got := index.IncludedMessages()[0].String(); got != "You are helpful." {
		t.Fatalf("first included message = %q, want system instructions", got)
	}
}

func TestToolResultStrategy_CompactAsyncExtractsMultipleToolNamesAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		{Role: message.RoleAssistant, Contents: message.Contents{
			&message.FunctionCallContent{CallID: "c1", Name: "get_weather"},
			&message.FunctionCallContent{CallID: "c2", Name: "search_docs"},
		}},
		textMessage(message.RoleTool, "Sunny"),
		textMessage(message.RoleTool, "Found 3 docs"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	const want = "[Tool Calls]\nget_weather:\n  - Sunny\nsearch_docs:\n  - Found 3 docs"
	if got := index.IncludedMessages()[1].String(); got != want {
		t.Fatalf("collapsed tool summary = %q, want %q", got, want)
	}
}

func TestToolResultStrategy_CompactAsyncNoToolGroupsReturnsFalseAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(0)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Hello"),
		textMessage(message.RoleAssistant, "Hi!"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("text-only groups should not be collapsed")
	}
}

func TestToolResultStrategy_CompactAsyncCompoundTriggerRequiresTokensAndToolCallsAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{
		Trigger:                compaction.All(compaction.TokensExceed(0), compaction.HasToolCalls()),
		MinimumPreservedGroups: new(1),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		functionCallMessage("c1", "fn"),
		textMessage(message.RoleTool, "result"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("compound trigger should compact the tool group")
	}
}

func TestToolResultStrategy_CompactAsyncTargetStopsCollapsingEarlyAsync(t *testing.T) {
	collapseCount := 0
	strategy := &compaction.ToolResultStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Target: func(*compaction.MessageIndex) bool { collapseCount++; return collapseCount >= 1 },
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		functionCallMessage("c1", "fn1"),
		textMessage(message.RoleTool, "result1"),
		textMessage(message.RoleUser, "Q2"),
		functionCallMessage("c2", "fn2"),
		textMessage(message.RoleTool, "result2"),
		textMessage(message.RoleUser, "Q3"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	collapsed := 0
	for _, group := range index.Groups {
		if group.IsExcluded && group.Kind == compaction.GroupKindToolCall {
			collapsed++
		}
	}
	if collapsed != 1 {
		t.Fatalf("collapsed tool group count = %d, want 1", collapsed)
	}
}

func TestToolResultStrategy_CompactAsyncSkipsPreExcludedAndSystemGroupsAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(0)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "System prompt"),
		textMessage(message.RoleUser, "Q0"),
		functionCallMessage("c1", "fn"),
		textMessage(message.RoleTool, "Result 1"),
		textMessage(message.RoleUser, "Q1"),
	}, nil)
	index.Groups[len(index.Groups)-1].IsExcluded = true
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if index.Groups[0].IsExcluded {
		t.Fatal("system group was excluded")
	}
}

func TestToolResultStrategy_CompactAsyncDeduplicatesDuplicateToolNamesAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		{Role: message.RoleAssistant, Contents: message.Contents{
			&message.FunctionCallContent{CallID: "c1", Name: "get_weather"},
			&message.FunctionCallContent{CallID: "c2", Name: "get_weather"},
		}},
		textMessage(message.RoleTool, "Sunny"),
		textMessage(message.RoleTool, "Rainy"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	const want = "[Tool Calls]\nget_weather:\n  - Sunny\n  - Rainy"
	if got := index.IncludedMessages()[1].String(); got != want {
		t.Fatalf("collapsed tool summary = %q, want %q", got, want)
	}
}

func TestToolResultStrategy_CompactAsyncIncludesResultsFromFunctionResultContentAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		{Role: message.RoleAssistant, Contents: message.Contents{
			&message.FunctionCallContent{CallID: "c1", Name: "get_weather"},
			&message.FunctionCallContent{CallID: "c2", Name: "search_docs"},
		}},
		functionResultMessage("c1", "Sunny and 72°F"),
		functionResultMessage("c2", "Found 3 docs"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	const want = "[Tool Calls]\nget_weather:\n  - Sunny and 72°F\nsearch_docs:\n  - Found 3 docs"
	if got := index.IncludedMessages()[1].String(); got != want {
		t.Fatalf("collapsed tool summary = %q, want %q", got, want)
	}
}

func TestToolResultStrategy_CompactAsyncDeduplicatesWithFunctionResultContentAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		{Role: message.RoleAssistant, Contents: message.Contents{
			&message.FunctionCallContent{CallID: "c1", Name: "get_weather"},
			&message.FunctionCallContent{CallID: "c2", Name: "get_weather"},
			&message.FunctionCallContent{CallID: "c3", Name: "search_docs"},
		}},
		functionResultMessage("c1", "Sunny"),
		functionResultMessage("c2", "Rainy"),
		functionResultMessage("c3", "Found 3 docs"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	const want = "[Tool Calls]\nget_weather:\n  - Sunny\n  - Rainy\nsearch_docs:\n  - Found 3 docs"
	if got := index.IncludedMessages()[1].String(); got != want {
		t.Fatalf("collapsed tool summary = %q, want %q", got, want)
	}
}

func TestToolResultStrategy_CompactAsyncUsesCustomFormatterAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		ToolCallFormatter: func(group *compaction.MessageGroup) string {
			return fmt.Sprintf("[Collapsed: %d messages]", len(group.Messages))
		},
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		functionCallMessage("c1", "get_weather"),
		textMessage(message.RoleTool, "Sunny"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if got := index.IncludedMessages()[1].String(); got != "[Collapsed: 2 messages]" {
		t.Fatalf("custom summary = %q, want collapsed count", got)
	}
}

func TestToolResultStrategy_ToolCallFormatterPropertyIsNullWhenNoneProvided(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always()}
	if strategy.ToolCallFormatter != nil {
		t.Fatal("default ToolCallFormatter should be nil")
	}
}

func TestToolResultStrategy_ToolCallFormatterPropertyReturnsCustomFormatterWhenProvided(t *testing.T) {
	customFormatter := func(*compaction.MessageGroup) string { return "custom" }
	strategy := &compaction.ToolResultStrategy{Trigger: compaction.Always(), ToolCallFormatter: customFormatter}
	if strategy.ToolCallFormatter == nil || reflect.ValueOf(strategy.ToolCallFormatter).Pointer() != reflect.ValueOf(customFormatter).Pointer() {
		t.Fatal("ToolCallFormatter did not retain the supplied function")
	}
}

func TestToolResultStrategy_CompactAsyncCustomFormatterCanDelegateToDefaultAsync(t *testing.T) {
	strategy := &compaction.ToolResultStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		ToolCallFormatter: func(group *compaction.MessageGroup) string {
			return "CUSTOM_PREFIX\n" + compaction.DefaultToolCallFormatter(group)
		},
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		functionCallMessage("c1", "fn"),
		textMessage(message.RoleTool, "result"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	const want = "CUSTOM_PREFIX\n[Tool Calls]\nfn:\n  - result"
	if got := index.IncludedMessages()[1].String(); got != want {
		t.Fatalf("wrapped summary = %q, want %q", got, want)
	}
}

func TestSummarizationStrategy_CompactAsyncTriggerNotMetReturnsFalseAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.TokensExceed(100000), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "Summary of conversation.", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Hello"),
		textMessage(message.RoleAssistant, "Hi!"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("unexpected compaction below the token threshold")
	}
	if got := index.IncludedGroupCount(); got != 2 {
		t.Fatalf("IncludedGroupCount = %d, want 2", got)
	}
}

func TestSummarizationStrategy_CompactAsyncSummarizesOldGroupsAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "Key facts from earlier.", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "First question"),
		textMessage(message.RoleAssistant, "First answer"),
		textMessage(message.RoleUser, "Second question"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	included := index.IncludedMessages()
	if len(included) != 2 {
		t.Fatalf("included message count = %d, want 2", len(included))
	}
	if text := included[0].String(); !strings.Contains(text, "[Summary]") || !strings.Contains(text, "Key facts from earlier.") {
		t.Fatalf("summary text = %q, want marker and generated summary", text)
	}
	if got := included[1].String(); got != "Second question" {
		t.Fatalf("preserved text = %q, want Second question", got)
	}
}

func TestSummarizationStrategy_CompactAsyncPreservesSystemMessagesAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "Summary of conversation.", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "You are helpful."),
		textMessage(message.RoleUser, "Old question"),
		textMessage(message.RoleAssistant, "Old answer"),
		textMessage(message.RoleUser, "Recent question"),
	}, nil)
	compactIndex(t, strategy, index)
	first := index.IncludedMessages()[0]
	if first.Role != message.RoleSystem || first.String() != "You are helpful." {
		t.Fatalf("first included message = %#v, want original system message", first)
	}
}

func TestSummarizationStrategy_CompactAsyncInsertsSummaryGroupAtCorrectPositionAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "Summary text.", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "System prompt."),
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	pos := slices.IndexFunc(index.Groups, func(group *compaction.MessageGroup) bool {
		return group.Kind == compaction.GroupKindSummary
	})
	if pos != 1 {
		t.Fatalf("summary group position = %d, want 1", pos)
	}
	summary := index.Groups[pos].Messages[0]
	if !strings.Contains(summary.String(), "[Summary]") {
		t.Fatalf("summary text = %q, want summary marker", summary.String())
	}
	if _, ok := summary.AdditionalProperties["_is_summary"]; !ok {
		t.Fatal("summary message is missing the summary property")
	}
}

func TestSummarizationStrategy_CompactAsyncHandlesEmptyLlmResponseAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "   ", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	if got := index.IncludedMessages()[0].String(); !strings.Contains(got, "[Summary unavailable]") {
		t.Fatalf("summary text = %q, want unavailable message", got)
	}
}

func TestSummarizationStrategy_CompactAsyncNothingToSummarizeReturnsFalseAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(5),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "Summary of conversation.", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Hello"),
		textMessage(message.RoleAssistant, "Hi!"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("compaction should respect the preserved-group floor")
	}
}

func TestSummarizationStrategy_CompactAsyncUsesCustomPromptAsync(t *testing.T) {
	var captured []*message.Message
	const customPrompt = "Summarize in bullet points only."
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1), SummarizationPrompt: new(customPrompt),
		Summarizer: compaction.SummarizerFunc(func(_ context.Context, messages []*message.Message) (string, error) {
			captured = messages
			return "Custom summary.", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	if len(captured) != 2 {
		t.Fatalf("summarizer received %d messages, want 2", len(captured))
	}
	if captured[0].Role != message.RoleSystem || captured[0].String() != customPrompt {
		t.Fatalf("prompt message = %#v, want custom system prompt", captured[0])
	}
	if captured[1].Role != message.RoleUser || captured[1].String() != "Q1" {
		t.Fatalf("conversation message = %#v, want Q1 user message", captured[1])
	}
}

func TestSummarizationStrategy_CompactAsyncSetsExcludeReasonAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "Summary of conversation.", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Old"),
		textMessage(message.RoleUser, "New"),
	}, nil)
	compactIndex(t, strategy, index)
	pos := slices.IndexFunc(index.Groups, func(group *compaction.MessageGroup) bool { return group.IsExcluded })
	if pos < 0 {
		t.Fatal("no group was excluded")
	}
	if reason := index.Groups[pos].ExcludeReason; !strings.Contains(reason, "SummarizationStrategy") {
		t.Fatalf("ExcludeReason = %q, want strategy name", reason)
	}
}

func TestSummarizationStrategy_CompactAsyncTargetStopsMarkingEarlyAsync(t *testing.T) {
	exclusionCount := 0
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Target: func(*compaction.MessageIndex) bool { exclusionCount++; return exclusionCount >= 1 },
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "Partial summary.", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleUser, "Q3"),
	}, nil)
	compactIndex(t, strategy, index)
	excluded := 0
	for _, group := range index.Groups {
		if group.IsExcluded {
			excluded++
		}
	}
	if excluded != 1 {
		t.Fatalf("excluded group count = %d, want 1", excluded)
	}
}

func TestSummarizationStrategy_CompactAsyncWithSystemBetweenSummarizableGroupsAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "[Summary]", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleSystem, "System note"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if index.Groups[0].Kind != compaction.GroupKindSummary || index.Groups[2].Kind != compaction.GroupKindSystem || index.Groups[2].IsExcluded {
		t.Fatalf("summary/system placement = %#v", index.Groups)
	}
}

func TestSummarizationStrategy_CompactAsyncMaxSummarizableBoundsLoopExitAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(3), Target: compaction.Never(),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "[Summary]", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
		textMessage(message.RoleUser, "Q3"),
		textMessage(message.RoleAssistant, "A3"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if got := index.IncludedGroupCount(); got != 4 {
		t.Fatalf("IncludedGroupCount = %d, want 4", got)
	}
}

func TestSummarizationStrategy_CompactAsyncWithPreExcludedGroupAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "[Summary]", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
	}, nil)
	index.Groups[0].IsExcluded = true
	if !compactIndex(t, strategy, index) {
		t.Fatal("expected compaction")
	}
	if !index.Groups[0].IsExcluded {
		t.Fatal("pre-excluded group was restored")
	}
}

func TestSummarizationStrategy_CompactAsyncWithEmptyTextMessageInGroupAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "[Summary]", nil
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		functionCallMessage("c1", "fn"),
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
	}, nil)
	if !compactIndex(t, strategy, index) {
		t.Fatal("tool-call content without text should still be summarizable")
	}
}

func TestSummarizationStrategy_CompactAsyncLlmFailureRestoresGroupsAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "", errors.New("Service unavailable")
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	originalGroupCount := len(index.Groups)
	if compactIndex(t, strategy, index) {
		t.Fatal("failed summarization should report no compaction")
	}
	if len(index.Groups) != originalGroupCount {
		t.Fatalf("group count = %d, want unchanged %d", len(index.Groups), originalGroupCount)
	}
	for i, group := range index.Groups {
		if group.IsExcluded || group.ExcludeReason != "" {
			t.Fatalf("group %d was not restored: %#v", i, group)
		}
	}
}

func TestSummarizationStrategy_CompactAsyncLlmFailureDoesNotInsertSummaryGroupAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "", errors.New("API error")
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	compactIndex(t, strategy, index)
	if slices.ContainsFunc(index.Groups, func(group *compaction.MessageGroup) bool { return group.Kind == compaction.GroupKindSummary }) {
		t.Fatal("failed summarization inserted a summary group")
	}
}

func TestSummarizationStrategy_CompactAsyncCancellationPropagatesAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "", context.Canceled
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if _, err := strategy.Compact(t.Context(), index); !errors.Is(err, context.Canceled) {
		t.Fatalf("Compact error = %v, want cancellation", err)
	}
}

func TestSummarizationStrategy_CompactAsyncTaskCancellationPropagatesAsync(t *testing.T) {
	canceled := fmt.Errorf("Task cancelled: %w", context.Canceled)
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "", canceled
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if _, err := strategy.Compact(t.Context(), index); err != canceled {
		t.Fatalf("Compact error = %v, want original wrapped cancellation %v", err, canceled)
	}
}

func TestSummarizationStrategy_CompactAsyncLlmFailureWithMultipleExcludedGroupsRestoresAllAsync(t *testing.T) {
	strategy := &compaction.SummarizationStrategy{
		Trigger: compaction.Always(), MinimumPreservedGroups: new(1), Target: compaction.Never(),
		Summarizer: compaction.SummarizerFunc(func(context.Context, []*message.Message) (string, error) {
			return "", errors.New("Rate limited")
		}),
	}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "System prompt"),
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
		textMessage(message.RoleUser, "Q3"),
	}, nil)
	if compactIndex(t, strategy, index) {
		t.Fatal("failed summarization should report no compaction")
	}
	for i, group := range index.Groups {
		if group.IsExcluded || group.ExcludeReason != "" {
			t.Fatalf("group %d was not restored: %#v", i, group)
		}
	}
	if got := index.IncludedGroupCount(); got != 6 {
		t.Fatalf("IncludedGroupCount = %d, want 6", got)
	}
}

func TestCompactionProvider_ConstructorThrowsOnNullStrategy(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewContextProvider should panic for a nil strategy")
		}
	}()
	compaction.NewContextProvider(compaction.ContextProviderConfig{})
}

func TestCompactionProvider_InvokingAsyncNullMessagesPassesThroughAsync(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{Trigger: compaction.TokensExceed(100000)},
	})
	session := agenttest.CreateSession()
	messages, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if messages != nil {
		t.Fatalf("messages = %#v, want nil", messages)
	}
}

func TestCompactionProvider_InvokingAsyncAppliesCompactionWhenTriggeredAsync(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)},
	})
	session := agenttest.CreateSession()
	messages := []*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}
	result, _, err := invokeProvider(provider, t.Context(), messages, agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result) >= len(messages) {
		t.Fatalf("compacted messages = %#v, want a non-nil reduced list", result)
	}
}

func TestCompactionProvider_InvokingAsyncNoCompactionNeededReturnsOriginalMessagesAsync(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{Trigger: compaction.TokensExceed(100000)},
	})
	session := agenttest.CreateSession()
	messages := []*message.Message{textMessage(message.RoleUser, "Hello")}
	result, _, err := invokeProvider(provider, t.Context(), messages, agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].String() != "Hello" {
		t.Fatalf("result = %#v, want one Hello message", result)
	}
}

func TestCompactionProvider_InvokingAsyncPreservesInstructionsAndToolsAsync(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{Trigger: compaction.TokensExceed(100000)},
	})
	session := agenttest.CreateSession()
	messages := []*message.Message{textMessage(message.RoleUser, "Hello")}
	function := functool.MustNew(functool.Config{Name: "MyTool"}, func(context.Context, struct{}) (string, error) {
		return "tool", nil
	})
	options := []agent.Option{agent.WithSession(session), agent.WithInstructions("Be helpful"), agent.WithTool(function)}
	_, resultOptions, err := invokeProvider(provider, t.Context(), messages, options...)
	if err != nil {
		t.Fatal(err)
	}
	instructions := slices.Collect(agent.AllOptions(resultOptions, agent.WithInstructions))
	if !slices.Equal(instructions, []string{"Be helpful"}) {
		t.Fatalf("instructions = %v, want Be helpful", instructions)
	}
	tools := slices.Collect(agent.AllOptions(resultOptions, agent.WithTool))
	if len(tools) != 1 || tools[0] != function {
		t.Fatalf("tools = %#v, want the original tool", tools)
	}
	if len(resultOptions) != len(options) || &resultOptions[0] != &options[0] {
		t.Fatal("provider did not preserve the options containing the tools")
	}
}

func TestCompactionProvider_InvokingAsyncWithExistingIndexUpdatesAsync(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)},
	})
	session := agenttest.CreateSession()
	first := []*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}
	if _, _, err := invokeProvider(provider, t.Context(), first, agent.WithSession(session)); err != nil {
		t.Fatal(err)
	}
	second := []*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
		textMessage(message.RoleAssistant, "A2"),
		textMessage(message.RoleUser, "Q3"),
	}
	result, _, err := invokeProvider(provider, t.Context(), second, agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result) > len(second) {
		t.Fatalf("updated messages = %#v, want non-nil list no larger than input", result)
	}
}

func TestCompactionProvider_CompactAsyncThrowsOnNullStrategyAsync(t *testing.T) {
	messages := []*message.Message{textMessage(message.RoleUser, "Hello")}
	defer func() {
		if recover() == nil {
			t.Fatal("Compact should panic for a nil strategy")
		}
	}()
	_, _ = compaction.Compact(t.Context(), nil, messages, nil)
}

func TestCompactionProvider_CompactAsyncReturnsAllMessagesWhenTriggerDoesNotFireAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.TokensExceed(100000)}
	messages := []*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}
	result, err := compaction.Compact(t.Context(), strategy, messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != len(messages) {
		t.Fatalf("message count = %d, want %d", len(result), len(messages))
	}
	if got, want := messageTexts(result), []string{"Q1", "A1", "Q2"}; !slices.Equal(got, want) {
		t.Fatalf("messages = %v, want %v", got, want)
	}
}

func TestCompactionProvider_CompactAsyncReducesMessagesWhenTriggeredAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	messages := []*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}
	result, err := compaction.Compact(t.Context(), strategy, messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) >= len(messages) {
		t.Fatalf("message count = %d, want fewer than %d", len(result), len(messages))
	}
}

func TestCompactionProvider_CompactAsyncHandlesEmptyMessageListAsync(t *testing.T) {
	strategy := &compaction.TruncationStrategy{Trigger: compaction.Always(), MinimumPreservedGroups: new(1)}
	result, err := compaction.Compact(t.Context(), strategy, []*message.Message{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatalf("messages = %#v, want empty", result)
	}
}

func TestCompactionProvider_InvokingAsyncMarksOnlyPreviouslySeenMessagesAsChatHistoryAsync(t *testing.T) {
	provider := compaction.NewContextProvider(compaction.ContextProviderConfig{
		Strategy: &compaction.TruncationStrategy{Trigger: compaction.TokensExceed(100000)},
	})
	session := agenttest.CreateSession()
	q1, a1, q2 := textMessage(message.RoleUser, "Q1"), textMessage(message.RoleAssistant, "A1"), textMessage(message.RoleUser, "Q2")
	first, _, err := invokeProvider(provider, t.Context(), []*message.Message{q1, a1, q2}, agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("first invocation returned %d messages, want 3", len(first))
	}
	for i, msg := range first {
		if msg.Source.Type == agent.SourceTypeHistoryProvider {
			t.Fatalf("first invocation message %d was marked as history", i)
		}
	}

	a2, q3 := textMessage(message.RoleAssistant, "A2"), textMessage(message.RoleUser, "Q3")
	second, _, err := invokeProvider(provider, t.Context(), []*message.Message{q1, a1, q2, a2, q3}, agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 5 {
		t.Fatalf("second invocation returned %d messages, want 5", len(second))
	}
	for i, msg := range second {
		if history := msg.Source.Type == agent.SourceTypeHistoryProvider; history != (i < 3) {
			t.Fatalf("second invocation message %d history = %v, want %v", i, history, i < 3)
		}
	}

	a3, q4 := textMessage(message.RoleAssistant, "A3"), textMessage(message.RoleUser, "Q4")
	third, _, err := invokeProvider(provider, t.Context(), []*message.Message{q1, a1, q2, a2, q3, a3, q4}, agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 7 {
		t.Fatalf("third invocation returned %d messages, want 7", len(third))
	}
	for i, msg := range third {
		if history := msg.Source.Type == agent.SourceTypeHistoryProvider; history != (i < 5) {
			t.Fatalf("third invocation message %d history = %v, want %v", i, history, i < 5)
		}
	}
}
