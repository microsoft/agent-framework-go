// Copyright (c) Microsoft. All rights reserved.

package agent

import "github.com/microsoft/agent-framework/go/content"

// Message represents a message in a conversation.
type Message struct {
	Contents  []content.Content
	Role      Role
	Name      string
	MessageID string
}

// NewMessage creates a new [Message] with the given role and contents.
func NewMessage(role Role, contents ...content.Content) *Message {
	return &Message{
		Role:     role,
		Contents: contents,
	}
}

// NewTextMessage creates a new [Message] with text content.
func NewTextMessage(text string) *Message {
	return &Message{
		Role:     RoleUser,
		Contents: []content.Content{&content.Text{Text: text}},
	}
}

// Text returns the first text content in the response, or empty string.
func (m *Message) Text() string {
	if m == nil {
		return ""
	}
	for _, c := range m.Contents {
		if textContent, ok := c.(*content.Text); ok {
			return textContent.Text
		}
	}
	return ""
}
