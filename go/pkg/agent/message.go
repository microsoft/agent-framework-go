// Copyright (c) Microsoft. All rights reserved.

package agent

// Message represents a message in a conversation.
type Message struct {
	Role     Role
	Contents []Content
	Name     string // Optional name of the message sender
}

// NewMessage creates a new [Message] with the given role and contents.
func NewMessage(role Role, contents ...Content) *Message {
	return &Message{
		Role:     role,
		Contents: contents,
	}
}

// NewTextMessage creates a new [Message] with text content.
func NewTextMessage(text string) *Message {
	return &Message{
		Role:     RoleUser,
		Contents: []Content{&TextContent{Text: text}},
	}
}

// Text returns the first text content in the response, or empty string.
func (m *Message) Text() string {
	for _, content := range m.Contents {
		if textContent, ok := content.(*TextContent); ok {
			return textContent.Text
		}
	}
	return ""
}
