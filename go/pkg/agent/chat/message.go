// Copyright (c) Microsoft. All rights reserved.

package chat

import (
	"context"

	"github.com/microsoft/agent-framework/go/pkg/agent"
)

// Message represents a message in a conversation.
type Message struct {
	Role     agent.Role
	Contents []agent.Content
	Name     string // Optional name of the message sender
}

// NewMessage creates a new ChatMessage with text content.
func NewMessage(role agent.Role, text string) *Message {
	return &Message{
		Role:     role,
		Contents: []agent.Content{&agent.TextContent{Text: text}},
	}
}

// Text returns the first text content in the response, or empty string.
func (m *Message) Text() string {
	for _, content := range m.Contents {
		if textContent, ok := content.(*agent.TextContent); ok {
			return textContent.Text
		}
	}
	return ""
}

// AddContent adds content to the message.
func (m *Message) AddContent(content agent.Content) {
	m.Contents = append(m.Contents, content)
}

// Response represents a response from an agent or chat client.
type Response struct {
	Message      *Message
	FinishReason agent.FinishReason
	Usage        *agent.UsageDetails
	ModelID      string
}

// Text returns the first text content in the response, or empty string.
func (r *Response) Text() string {
	if r.Message == nil {
		return ""
	}
	for _, content := range r.Message.Contents {
		if textContent, ok := content.(*agent.TextContent); ok {
			return textContent.Text
		}
	}
	return ""
}

// ResponseUpdate represents a streaming update from an agent or chat client.
type ResponseUpdate struct {
	Delta        *Message
	FinishReason agent.FinishReason
	Usage        *agent.UsageDetails
	ModelID      string
}

// InMemoryMessageStore is a simple in-memory implementation of MessageStore.
type InMemoryMessageStore struct {
	store map[string][]*Message
}

// NewInMemoryMessageStore creates a new InMemoryMessageStore.
func NewInMemoryMessageStore() *InMemoryMessageStore {
	return &InMemoryMessageStore{
		store: make(map[string][]*Message),
	}
}

// Save stores messages.
func (s *InMemoryMessageStore) Save(ctx context.Context, threadID string, messages ...*Message) error {
	s.store[threadID] = messages
	return nil
}

// Load retrieves messages for a thread.
func (s *InMemoryMessageStore) Load(ctx context.Context, threadID string) ([]*Message, error) {
	messages, ok := s.store[threadID]
	if !ok {
		return []*Message{}, nil
	}
	return messages, nil
}

// Delete removes messages for a thread.
func (s *InMemoryMessageStore) Delete(ctx context.Context, threadID string) error {
	delete(s.store, threadID)
	return nil
}
