// Copyright (c) Microsoft. All rights reserved.

package compaction

import "github.com/microsoft/agent-framework-go/message"

// GroupKind identifies the role a message group plays during compaction.
type GroupKind int

const (
	// GroupKindSystem contains one or more system messages.
	GroupKindSystem GroupKind = iota
	// GroupKindUser contains a single user message.
	GroupKindUser
	// GroupKindAssistantText contains a single assistant text response without tool calls.
	GroupKindAssistantText
	// GroupKindToolCall contains an assistant tool call and its matching tool result messages.
	GroupKindToolCall
	// GroupKindSummary contains a summary message produced by compaction.
	GroupKindSummary
)

const summaryPropertyKey = "_is_summary"

// MessageGroup represents a logical group of messages that must be kept or removed together.
//
// Groups preserve atomic relationships such as an assistant tool call and its corresponding tool
// result messages. They can be marked as excluded so compacted projections omit them while the
// original grouped history remains available for diagnostics, storage, or later re-inclusion.
type MessageGroup struct {
	// Kind is the kind of this message group.
	Kind GroupKind

	// Messages contains the messages in this group.
	Messages []*message.Message

	// MessageCount is the number of messages in this group.
	MessageCount int

	// ByteCount is the total UTF-8 byte count of this group's message content.
	ByteCount int

	// TokenCount is the estimated or counted token count for this group's messages.
	TokenCount int

	// TurnIndex identifies the user turn this group belongs to.
	//
	// System groups have a nil turn index. A turn starts with a user group and includes subsequent
	// non-user, non-system groups until the next user group or end of conversation.
	TurnIndex *int `json:",omitzero"`

	// IsExcluded indicates whether this group is omitted from the projected message list.
	IsExcluded bool

	// ExcludeReason optionally explains why this group was excluded.
	ExcludeReason string `json:",omitzero"`
}

func newMessageGroup(kind GroupKind, messages []*message.Message, byteCount, tokenCount int, turnIndex *int) *MessageGroup {
	return &MessageGroup{
		Kind:         kind,
		Messages:     messages,
		MessageCount: len(messages),
		ByteCount:    byteCount,
		TokenCount:   tokenCount,
		TurnIndex:    cloneTurnIndex(turnIndex),
	}
}

func cloneTurnIndex(turnIndex *int) *int {
	if turnIndex == nil {
		return nil
	}
	v := *turnIndex
	return &v
}
