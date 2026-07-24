// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/message"
)

const defaultMinimumPreservedToolResultGroups = 16

// ToolResultStrategy collapses old tool-call groups into assistant summary messages.
//
// This strategy preserves user messages and plain assistant responses. It only targets tool-call
// groups outside the protected recent window and replaces each with a concise assistant summary.
type ToolResultStrategy struct {
	// Trigger controls whether tool-result compaction should run.
	// When nil, compaction always runs.
	Trigger Trigger

	// Target controls when compaction stops after each collapsed tool-call group.
	// When nil, compaction stops when Trigger would no longer fire.
	Target Trigger

	// MinimumPreservedGroups is the minimum number of most-recent non-system groups to preserve.
	// This is a hard floor; tool-call groups within this protected window are not collapsed.
	MinimumPreservedGroups int

	// ToolCallFormatter formats a tool-call group as a compact summary string.
	// When nil, DefaultToolCallFormatter is used, which produces a YAML-like block listing
	// each tool name and its results.
	ToolCallFormatter func(*MessageGroup) string
}

// Compact compacts index in place.
func (strategy *ToolResultStrategy) Compact(_ context.Context, index *MessageIndex) (bool, error) {
	target, ok := prepareCompaction(index, strategy.Trigger, strategy.Target)
	if !ok {
		return false, nil
	}

	minimumPreservedGroups := cmp.Or(max(strategy.MinimumPreservedGroups, 0), defaultMinimumPreservedToolResultGroups)
	var nonSystemIncludedIndices []int
	for i, group := range index.Groups {
		if !group.IsExcluded && group.Kind != GroupKindSystem {
			nonSystemIncludedIndices = append(nonSystemIncludedIndices, i)
		}
	}
	protectedStart := len(nonSystemIncludedIndices) - minimumPreservedGroups
	if protectedStart < 0 {
		protectedStart = 0
	}
	protectedGroupIndices := nonSystemIncludedIndices[protectedStart:]

	var eligibleIndices []int
	for i, group := range index.Groups {
		if !group.IsExcluded && group.Kind == GroupKindToolCall && !slices.Contains(protectedGroupIndices, i) {
			eligibleIndices = append(eligibleIndices, i)
		}
	}
	if len(eligibleIndices) == 0 {
		return false, nil
	}

	formatter := strategy.ToolCallFormatter
	if formatter == nil {
		formatter = DefaultToolCallFormatter
	}
	compacted := false
	offset := 0
	for _, eligibleIndex := range eligibleIndices {
		idx := eligibleIndex + offset
		group := index.Groups[idx]
		summary := formatter(group)

		group.IsExcluded = true
		group.ExcludeReason = "collapsed by ToolResultStrategy"

		summaryMessage := &message.Message{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.TextContent{Text: summary},
			},
			AdditionalProperties: map[string]any{summaryPropertyKey: true},
		}
		index.InsertGroup(idx+1, GroupKindSummary, []*message.Message{summaryMessage}, group.TurnIndex)
		offset++
		compacted = true
		if target(index) {
			break
		}
	}
	return compacted, nil
}

// DefaultToolCallFormatter produces a YAML-like summary of tool-call groups, including tool names,
// results, and deduplication counts for repeated tool names.
//
// This is the formatter used when no custom ToolCallFormatter is supplied. It can be referenced
// directly in a custom formatter to augment or wrap the default output.
func DefaultToolCallFormatter(group *MessageGroup) string {
	type call struct {
		id   string
		name string
	}
	var functionCalls []call
	resultsByCallID := make(map[string]string)
	var plainTextResults []string

	for _, msg := range group.Messages {
		hasFunctionResult := false
		for _, content := range msg.Contents {
			switch typed := content.(type) {
			case *message.FunctionCallContent:
				functionCalls = append(functionCalls, call{id: typed.CallID, name: typed.Name})
			case *message.FunctionResultContent:
				resultsByCallID[typed.CallID] = fmt.Sprint(typed.Result)
				hasFunctionResult = true
			}
		}
		if !hasFunctionResult && msg.Role == message.RoleTool {
			if text := msg.String(); text != "" {
				plainTextResults = append(plainTextResults, text)
			}
		}
	}

	plainTextIndex := 0
	var orderedNames []string
	seenNames := make(map[string]bool)
	groupedResults := make(map[string][]string)
	for _, functionCall := range functionCalls {
		if !seenNames[functionCall.name] {
			seenNames[functionCall.name] = true
			orderedNames = append(orderedNames, functionCall.name)
		}
		result, ok := resultsByCallID[functionCall.id]
		if !ok && plainTextIndex < len(plainTextResults) {
			result = plainTextResults[plainTextIndex]
			plainTextIndex++
		}
		if result != "" {
			groupedResults[functionCall.name] = append(groupedResults[functionCall.name], result)
		}
	}

	lines := []string{"[Tool Calls]"}
	for _, name := range orderedNames {
		lines = append(lines, name+":")
		for _, result := range groupedResults[name] {
			lines = append(lines, "  - "+result)
		}
	}
	return strings.Join(lines, "\n")
}
