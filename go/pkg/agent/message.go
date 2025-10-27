// Copyright (c) Microsoft. All rights reserved.

package agent

// Message represents a message in a conversation.
type Message struct {
	Role     Role
	Contents []Content
	Name     string // Optional name of the message sender
}

// NewMessage creates a new ChatMessage with text content.
func NewMessage(role Role, text string) *Message {
	return &Message{
		Role:     role,
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

// AddContent adds content to the message.
func (m *Message) AddContent(content Content) {
	m.Contents = append(m.Contents, content)
}
