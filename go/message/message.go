// Copyright (c) Microsoft. All rights reserved.

package message

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

// Message represents a message in a conversation.
type Message struct {
	Contents  Contents
	Role      Role
	Name      string
	MessageID string
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
	return &Message{
		Role:     RoleUser,
		Contents: []Content{&TextContent{Text: text}},
	}
}

// Text returns the first text content in the response, or empty string.
func (m *Message) Text() string {
	if m == nil {
		return ""
	}
	for _, c := range m.Contents {
		if textContent, ok := c.(*TextContent); ok {
			return textContent.Text
		}
	}
	return ""
}
