// Copyright (c) Microsoft. All rights reserved.

package message

import (
	"maps"
	"time"
)

// Role represents the role of a message sender in a conversation.
type Role string

const (
	// RoleUser represents a message from the user.
	RoleUser Role = "user"
	// RoleAssistant represents a message from the assistant.
	RoleAssistant Role = "assistant"
	// RoleSystem represents a system message.
	RoleSystem Role = "system"
	// RoleTool represents a message from a tool execution.
	RoleTool Role = "tool"
)

// SourceType represents the type of component that generated a message.
type SourceType string

// SourceTypeExternal is the zero source type for messages that originated outside the agent pipeline.
const SourceTypeExternal SourceType = ""

// Source represents attribution information for the source of a message.
type Source struct {
	// ID is the unique identifier of the source that generated the message.
	ID string `json:",omitzero"`

	// Type identifies the kind of component that generated the message.
	Type SourceType `json:",omitzero"`
}

// Message represents a message in a conversation.
type Message struct {
	AdditionalProperties map[string]any `json:",omitzero"`
	Contents             Contents
	Role                 Role
	ID                   string
	AuthorName           string    `json:",omitzero"`
	Source               Source    `json:",omitzero"`
	CreatedAt            time.Time `json:",omitzero"`
	RawRepresentation    any       `json:"-"`
}

// New creates a new [Message] with the given role and contents.
func New(contents ...Content) *Message {
	return &Message{
		Role:     RoleUser,
		Contents: contents,
	}
}

// NewText creates a new [Message] with text content.
func NewText(text string) *Message {
	return New(&TextContent{Text: text})
}

func (m *Message) String() string {
	return m.Contents.Text()
}

func (m *Message) Usage() UsageDetails {
	return m.Contents.Usage()
}

// Clone creates a shallow copy of the message.
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	v := *m
	v.AdditionalProperties = maps.Clone(m.AdditionalProperties)
	return &v
}
