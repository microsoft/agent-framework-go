// Copyright (c) Microsoft. All rights reserved.

package chat

import (
	"context"

	"github.com/microsoft/agent-framework/go/pkg/agent"
)

// Response represents a response from an agent or chat client.
type Response struct {
	Message      *agent.Message
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
	Delta        *agent.Message
	FinishReason agent.FinishReason
	Usage        *agent.UsageDetails
	ModelID      string
}

// InMemoryMessageStore is a simple in-memory implementation of MessageStore.
type InMemoryMessageStore struct {
	store map[string][]*agent.Message
}

// NewInMemoryMessageStore creates a new InMemoryMessageStore.
func NewInMemoryMessageStore() *InMemoryMessageStore {
	return &InMemoryMessageStore{
		store: make(map[string][]*agent.Message),
	}
}

// Save stores messages.
func (s *InMemoryMessageStore) Save(ctx context.Context, threadID string, messages ...*agent.Message) error {
	s.store[threadID] = messages
	return nil
}

// Load retrieves messages for a thread.
func (s *InMemoryMessageStore) Load(ctx context.Context, threadID string) ([]*agent.Message, error) {
	messages, ok := s.store[threadID]
	if !ok {
		return []*agent.Message{}, nil
	}
	return messages, nil
}

// Delete removes messages for a thread.
func (s *InMemoryMessageStore) Delete(ctx context.Context, threadID string) error {
	delete(s.store, threadID)
	return nil
}
