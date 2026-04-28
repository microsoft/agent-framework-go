// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"cmp"
	"context"
	"errors"
	"strings"

	"github.com/microsoft/agent-framework-go/message"
)

const defaultSummarizationPrompt = `You are a conversation summarizer. Produce a concise summary of the conversation that preserves:

- Key facts, decisions, and user preferences
- Important context needed for future turns
- Tool call outcomes and their significance

Omit pleasantries and redundant exchanges. Be factual and brief.`

const defaultMinimumPreservedSummarizationGroups = 8

// Summarizer generates a summary from messages selected by a summarization strategy.
type Summarizer interface {
	// Summarize returns a textual summary of messages.
	Summarize(context.Context, []*message.Message) (string, error)
}

// SummarizerFunc adapts a function to Summarizer.
type SummarizerFunc func(context.Context, []*message.Message) (string, error)

// Summarize calls f(ctx, messages).
func (f SummarizerFunc) Summarize(ctx context.Context, messages []*message.Message) (string, error) {
	return f(ctx, messages)
}

// SummarizationStrategy summarizes older groups into a single assistant summary message.
//
// The strategy protects system messages and the most recent non-system groups. Older groups are sent
// to Summarizer, and the resulting summary is inserted as a GroupKindSummary message.
type SummarizationStrategy struct {
	// Trigger controls whether summarization should run.
	// When nil, summarization always runs.
	Trigger Trigger

	// Target controls when summarization stops marking groups after each exclusion.
	// When nil, summarization stops when Trigger would no longer fire.
	Target Trigger

	// Summarizer generates the replacement summary text.
	// When nil, the strategy performs no compaction.
	Summarizer Summarizer

	// MinimumPreservedGroups is the minimum number of most-recent non-system groups to preserve.
	// This is a hard floor; summarization will not summarize groups within this protected window.
	MinimumPreservedGroups int

	// SummarizationPrompt is the system prompt prepended to messages sent to Summarizer.
	// When empty, a default prompt is used.
	SummarizationPrompt string

	// SummaryUnavailableMessage is used when Summarizer returns only whitespace.
	// When empty, a default unavailable message is used.
	SummaryUnavailableMessage string
}

// Compact compacts index in place.
func (strategy *SummarizationStrategy) Compact(ctx context.Context, index *MessageIndex) (bool, error) {
	target, ok := prepareCompaction(index, strategy.Trigger, strategy.Target)
	if !ok {
		return false, nil
	}

	if strategy.Summarizer == nil {
		return false, nil
	}
	minimumPreservedGroups := cmp.Or(max(strategy.MinimumPreservedGroups, 0), defaultMinimumPreservedSummarizationGroups)
	summarizationPrompt := cmp.Or(strategy.SummarizationPrompt, defaultSummarizationPrompt)
	summaryUnavailableMessage := cmp.Or(strategy.SummaryUnavailableMessage, "[Summary unavailable]")

	nonSystemIncludedCount := 0
	for _, group := range index.Groups {
		if !group.IsExcluded && group.Kind != GroupKindSystem {
			nonSystemIncludedCount++
		}
	}
	protectedFromEnd := min(minimumPreservedGroups, nonSystemIncludedCount)
	maxSummarizable := nonSystemIncludedCount - protectedFromEnd
	if maxSummarizable <= 0 {
		return false, nil
	}

	summarizationMessages := []*message.Message{{
		Role: message.RoleSystem,
		Contents: []message.Content{
			&message.TextContent{Text: summarizationPrompt},
		},
	}}
	var excludedGroups []*MessageGroup
	insertIndex := -1
	for i, group := range index.Groups {
		if len(excludedGroups) >= maxSummarizable {
			break
		}
		if group.IsExcluded || group.Kind == GroupKindSystem {
			continue
		}
		if insertIndex < 0 {
			insertIndex = i
		}
		summarizationMessages = append(summarizationMessages, group.Messages...)
		group.IsExcluded = true
		group.ExcludeReason = "summarized by SummarizationStrategy"
		excludedGroups = append(excludedGroups, group)
		if target(index) {
			break
		}
	}

	summaryText, err := strategy.Summarizer.Summarize(ctx, summarizationMessages)
	if err != nil {
		for _, group := range excludedGroups {
			group.IsExcluded = false
			group.ExcludeReason = ""
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return false, nil
	}
	if strings.TrimSpace(summaryText) == "" {
		summaryText = summaryUnavailableMessage
	}

	summaryMessage := &message.Message{
		Role: message.RoleAssistant,
		Contents: []message.Content{
			&message.TextContent{Text: "[Summary]\n" + summaryText},
		},
		AdditionalProperties: map[string]any{summaryPropertyKey: true},
	}
	index.InsertGroup(insertIndex, GroupKindSummary, []*message.Message{summaryMessage}, nil)
	return true, nil
}
