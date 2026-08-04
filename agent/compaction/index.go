// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/microsoft/agent-framework-go/message"
)

// TokenCounter counts tokens for text-bearing content.
type TokenCounter interface {
	// CountTokens returns the token count for text content.
	CountTokens(string) int
}

// MessageIndex groups a flat message list into atomic units and tracks compaction metrics.
//
// Groups may be marked as excluded without being removed, allowing strategies to project a compacted
// message list while preserving the original grouped history for diagnostics and storage. Metrics are
// available for all groups and for the included, non-excluded subset.
type MessageIndex struct {
	// Groups is the ordered list of message groups in the index.
	Groups []*MessageGroup

	// TokenCounter is used to compute token counts for newly created groups.
	// When nil, token counts are estimated from byte counts.
	TokenCounter TokenCounter `json:"-"`

	currentTurn          int
	lastProcessedMessage *message.Message
}

// NewMessageIndex creates a message index from pre-built groups.
//
// The index restores its turn counter and last processed message from the provided groups so it can
// continue incremental updates when new messages are appended.
func NewMessageIndex(groups []*MessageGroup, tokenCounter TokenCounter) *MessageIndex {
	index := &MessageIndex{
		Groups:       groups,
		TokenCounter: tokenCounter,
	}
	for i := len(groups) - 1; i >= 0; i-- {
		group := groups[i]
		if index.lastProcessedMessage == nil && group.Kind != GroupKindSummary && len(group.Messages) > 0 {
			index.lastProcessedMessage = group.Messages[len(group.Messages)-1]
		}
		if group.TurnIndex != nil {
			index.currentTurn = *group.TurnIndex
			if index.lastProcessedMessage != nil {
				break
			}
		}
	}
	return index
}

// CreateMessageIndex creates a message index from a flat message list.
//
// The grouping algorithm preserves system messages, user turns, assistant text, summaries, and
// assistant tool-call/result pairs as logical groups.
func CreateMessageIndex(messages []*message.Message, tokenCounter TokenCounter) *MessageIndex {
	index := NewMessageIndex(nil, tokenCounter)
	index.appendFromMessages(messages, 0)
	return index
}

// Update incrementally appends new messages or rebuilds the index when the existing prefix changed.
//
// Existing groups and exclusion state are preserved when the previous last processed message is still
// present. If the message list was replaced or trimmed before that point, the index is rebuilt.
func (index *MessageIndex) Update(messages []*message.Message) {
	if len(messages) == 0 {
		index.Groups = nil
		index.currentTurn = 0
		index.lastProcessedMessage = nil
		return
	}
	processedMessageCount := index.includedRawMessageCount()
	if index.lastProcessedMessage != nil && len(messages) >= processedMessageCount && messageContentEqual(messages[len(messages)-1], index.lastProcessedMessage) {
		return
	}

	foundIndex := -1
	if index.lastProcessedMessage != nil {
		for i := len(messages) - 1; i >= 0; i-- {
			if messageContentEqual(messages[i], index.lastProcessedMessage) {
				foundIndex = i
				break
			}
		}
	}
	if foundIndex < 0 || foundIndex+1 < processedMessageCount {
		index.Groups = nil
		index.currentTurn = 0
		index.lastProcessedMessage = nil
		index.appendFromMessages(messages, 0)
		return
	}
	index.appendFromMessages(messages, foundIndex+1)
}

func (index *MessageIndex) appendFromMessages(messages []*message.Message, startIndex int) {
	for i := startIndex; i < len(messages); {
		msg := messages[i]
		switch {
		case msg.Role == message.RoleSystem:
			index.Groups = append(index.Groups, index.createGroup(GroupKindSystem, []*message.Message{msg}, nil))
			i++
		case msg.Role == message.RoleUser:
			index.currentTurn++
			turnIndex := index.currentTurn
			index.Groups = append(index.Groups, index.createGroup(GroupKindUser, []*message.Message{msg}, &turnIndex))
			i++
		case msg.Role == message.RoleAssistant && hasToolCalls(msg):
			turnIndex := index.currentTurn
			groupMessages := []*message.Message{msg}
			i++
			for i < len(messages) && (messages[i].Role == message.RoleTool || (messages[i].Role == message.RoleAssistant && hasOnlyReasoning(messages[i]))) {
				groupMessages = append(groupMessages, messages[i])
				i++
			}
			index.Groups = append(index.Groups, index.createGroup(GroupKindToolCall, groupMessages, &turnIndex))
		case msg.Role == message.RoleAssistant && isSummaryMessage(msg):
			turnIndex := index.currentTurn
			index.Groups = append(index.Groups, index.createGroup(GroupKindSummary, []*message.Message{msg}, &turnIndex))
			i++
		case msg.Role == message.RoleAssistant && hasOnlyReasoning(msg):
			lookahead := i + 1
			for lookahead < len(messages) && messages[lookahead].Role == message.RoleAssistant && hasOnlyReasoning(messages[lookahead]) {
				lookahead++
			}
			if lookahead < len(messages) && messages[lookahead].Role == message.RoleAssistant && hasToolCalls(messages[lookahead]) {
				turnIndex := index.currentTurn
				groupMessages := slices.Clone(messages[i : lookahead+1])
				i = lookahead + 1
				for i < len(messages) && (messages[i].Role == message.RoleTool || (messages[i].Role == message.RoleAssistant && hasOnlyReasoning(messages[i]))) {
					groupMessages = append(groupMessages, messages[i])
					i++
				}
				index.Groups = append(index.Groups, index.createGroup(GroupKindToolCall, groupMessages, &turnIndex))
			} else {
				turnIndex := index.currentTurn
				index.Groups = append(index.Groups, index.createGroup(GroupKindAssistantText, []*message.Message{msg}, &turnIndex))
				i++
			}
		default:
			turnIndex := index.currentTurn
			index.Groups = append(index.Groups, index.createGroup(GroupKindAssistantText, []*message.Message{msg}, &turnIndex))
			i++
		}
	}
	if len(messages) > 0 {
		index.lastProcessedMessage = messages[len(messages)-1]
	}
}

// InsertGroup creates and inserts a group at the specified index.
func (index *MessageIndex) InsertGroup(at int, kind GroupKind, messages []*message.Message, turnIndex *int) *MessageGroup {
	group := index.createGroup(kind, messages, turnIndex)
	index.Groups = slices.Insert(index.Groups, at, group)
	return group
}

// AddGroup creates and appends a group to the end of the index.
func (index *MessageIndex) AddGroup(kind GroupKind, messages []*message.Message, turnIndex *int) *MessageGroup {
	group := index.createGroup(kind, messages, turnIndex)
	index.Groups = append(index.Groups, group)
	return group
}

// IncludedMessages returns messages from groups that are not excluded.
func (index *MessageIndex) IncludedMessages() []*message.Message {
	var messages []*message.Message
	for _, group := range index.Groups {
		if !group.IsExcluded {
			messages = append(messages, group.Messages...)
		}
	}
	return messages
}

// AllMessages returns messages from all groups, including excluded groups.
func (index *MessageIndex) AllMessages() []*message.Message {
	var messages []*message.Message
	for _, group := range index.Groups {
		messages = append(messages, group.Messages...)
	}
	return messages
}

// TotalGroupCount returns the number of groups, including excluded groups.
func (index *MessageIndex) TotalGroupCount() int { return len(index.Groups) }

// TotalMessageCount returns the number of messages across all groups, including excluded groups.
func (index *MessageIndex) TotalMessageCount() int {
	var total int
	for _, group := range index.Groups {
		total += group.MessageCount
	}
	return total
}

// TotalByteCount returns the UTF-8 byte count across all groups, including excluded groups.
func (index *MessageIndex) TotalByteCount() int {
	var total int
	for _, group := range index.Groups {
		total += group.ByteCount
	}
	return total
}

// TotalTokenCount returns the token count across all groups, including excluded groups.
func (index *MessageIndex) TotalTokenCount() int {
	var total int
	for _, group := range index.Groups {
		total += group.TokenCount
	}
	return total
}

// IncludedGroupCount returns the number of groups that are not excluded.
func (index *MessageIndex) IncludedGroupCount() int {
	var total int
	for _, group := range index.Groups {
		if !group.IsExcluded {
			total++
		}
	}
	return total
}

// IncludedMessageCount returns the number of messages across non-excluded groups.
func (index *MessageIndex) IncludedMessageCount() int {
	var total int
	for _, group := range index.Groups {
		if !group.IsExcluded {
			total += group.MessageCount
		}
	}
	return total
}

// IncludedByteCount returns the UTF-8 byte count across non-excluded groups.
func (index *MessageIndex) IncludedByteCount() int {
	var total int
	for _, group := range index.Groups {
		if !group.IsExcluded {
			total += group.ByteCount
		}
	}
	return total
}

// IncludedTokenCount returns the token count across non-excluded groups.
func (index *MessageIndex) IncludedTokenCount() int {
	var total int
	for _, group := range index.Groups {
		if !group.IsExcluded {
			total += group.TokenCount
		}
	}
	return total
}

// TotalTurnCount returns the number of user turns across all groups.
func (index *MessageIndex) TotalTurnCount() int {
	seen := make(map[int]bool)
	for _, group := range index.Groups {
		if group.TurnIndex != nil && *group.TurnIndex > 0 {
			seen[*group.TurnIndex] = true
		}
	}
	return len(seen)
}

// IncludedTurnCount returns the number of user turns with at least one non-excluded group.
func (index *MessageIndex) IncludedTurnCount() int {
	seen := make(map[int]bool)
	for _, group := range index.Groups {
		if !group.IsExcluded && group.TurnIndex != nil && *group.TurnIndex > 0 {
			seen[*group.TurnIndex] = true
		}
	}
	return len(seen)
}

// IncludedNonSystemGroupCount returns the number of non-system groups that are not excluded.
func (index *MessageIndex) IncludedNonSystemGroupCount() int {
	var total int
	for _, group := range index.Groups {
		if !group.IsExcluded && group.Kind != GroupKindSystem {
			total++
		}
	}
	return total
}

// RawMessageCount returns the number of original messages represented by the index.
//
// Summary groups are excluded from this count because they are generated during compaction.
func (index *MessageIndex) RawMessageCount() int {
	var total int
	for _, group := range index.Groups {
		if group.Kind != GroupKindSummary {
			total += group.MessageCount
		}
	}
	return total
}

func (index *MessageIndex) includedRawMessageCount() int {
	var total int
	for _, group := range index.Groups {
		if !group.IsExcluded && group.Kind != GroupKindSummary {
			total += group.MessageCount
		}
	}
	return total
}

// TurnGroups returns all groups that belong to the specified user turn.
func (index *MessageIndex) TurnGroups(turnIndex int) []*MessageGroup {
	var groups []*MessageGroup
	for _, group := range index.Groups {
		if group.TurnIndex != nil && *group.TurnIndex == turnIndex {
			groups = append(groups, group)
		}
	}
	return groups
}

func (index *MessageIndex) createGroup(kind GroupKind, messages []*message.Message, turnIndex *int) *MessageGroup {
	byteCount := computeByteCount(messages)
	tokenCount := byteCount / 4
	if index.TokenCounter != nil {
		tokenCount = computeTokenCount(messages, index.TokenCounter)
	}
	return newMessageGroup(kind, messages, byteCount, tokenCount, turnIndex)
}

func computeByteCount(messages []*message.Message) int {
	var total int
	for _, msg := range messages {
		for _, content := range msg.Contents {
			total += computeContentByteCount(content)
		}
	}
	return total
}

func computeTokenCount(messages []*message.Message, tokenCounter TokenCounter) int {
	var total int
	for _, msg := range messages {
		for _, content := range msg.Contents {
			switch typed := content.(type) {
			case *message.TextContent:
				if typed.Text != "" {
					total += tokenCounter.CountTokens(typed.Text)
				}
			case *message.TextReasoningContent:
				if typed.Text != "" {
					total += tokenCounter.CountTokens(typed.Text)
				}
				if typed.ProtectedData != "" {
					total += tokenCounter.CountTokens(typed.ProtectedData)
				}
			default:
				total += computeContentByteCount(content) / 4
			}
		}
	}
	return total
}

func computeContentByteCount(content message.Content) int {
	switch typed := content.(type) {
	case *message.TextContent:
		return stringByteCount(typed.Text)
	case *message.TextReasoningContent:
		return stringByteCount(typed.Text) + stringByteCount(typed.ProtectedData)
	case *message.DataContent:
		return len(typed.Data) + stringByteCount(typed.MediaType) + stringByteCount(typed.Name)
	case *message.URIContent:
		return stringByteCount(typed.URI) + stringByteCount(typed.MediaType)
	case *message.FunctionCallContent:
		return stringByteCount(typed.CallID) + stringByteCount(typed.Name) + stringByteCount(typed.Arguments)
	case *message.FunctionResultContent:
		return stringByteCount(typed.CallID) + stringByteCount(fmt.Sprint(typed.Result))
	case *message.ErrorContent:
		return stringByteCount(typed.Message) + stringByteCount(typed.ErrorCode) + stringByteCount(typed.Details)
	case *message.HostedFileContent:
		return stringByteCount(typed.FileID) + stringByteCount(typed.MediaType) + stringByteCount(typed.Name)
	default:
		return 0
	}
}

func stringByteCount(value string) int { return len(value) }

func hasToolCalls(msg *message.Message) bool {
	return slices.ContainsFunc(msg.Contents, func(content message.Content) bool {
		_, ok := content.(*message.FunctionCallContent)
		return ok
	})
}

func hasOnlyReasoning(msg *message.Message) bool {
	return !slices.ContainsFunc(msg.Contents, func(content message.Content) bool {
		_, ok := content.(*message.TextReasoningContent)
		return !ok
	})
}

func isSummaryMessage(msg *message.Message) bool {
	if msg.AdditionalProperties == nil {
		return false
	}
	value, ok := msg.AdditionalProperties[summaryPropertyKey]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case json.RawMessage:
		return rawSummaryMessageValue(typed)
	case *json.RawMessage:
		return typed != nil && rawSummaryMessageValue(*typed)
	case []byte:
		return rawSummaryMessageValue(typed)
	default:
		return false
	}
}

func rawSummaryMessageValue(value []byte) bool {
	var boolValue bool
	if err := json.Unmarshal(value, &boolValue); err != nil {
		return false
	}
	return boolValue
}
