// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"

	"github.com/microsoft/agent-framework/go/pkg/message"
)

// ContextProvider provides additional context to agents.
type ContextProvider interface {
	// GetContext retrieves context based on the current messages.
	GetContext(ctx context.Context, messages ...*message.ChatMessage) ([]string, error)
}

// AggregateContextProvider combines multiple context providers.
type AggregateContextProvider struct {
	providers []ContextProvider
}

// NewAggregateContextProvider creates a new AggregateContextProvider.
func NewAggregateContextProvider(providers ...ContextProvider) *AggregateContextProvider {
	return &AggregateContextProvider{
		providers: providers,
	}
}

// GetContext retrieves context from all providers.
func (a *AggregateContextProvider) GetContext(ctx context.Context, messages ...*message.ChatMessage) ([]string, error) {
	var allContext []string
	for _, provider := range a.providers {
		context, err := provider.GetContext(ctx, messages...)
		if err != nil {
			return nil, err
		}
		allContext = append(allContext, context...)
	}
	return allContext, nil
}

// MessageStore persists and retrieves messages.
type MessageStore interface {
	// Save stores messages.
	Save(ctx context.Context, threadID string, messages ...*message.ChatMessage) error

	// Load retrieves messages for a thread.
	Load(ctx context.Context, threadID string) ([]*message.ChatMessage, error)

	// Delete removes messages for a thread.
	Delete(ctx context.Context, threadID string) error
}

// InMemoryMessageStore is a simple in-memory implementation of MessageStore.
type InMemoryMessageStore struct {
	store map[string][]*message.ChatMessage
}

// NewInMemoryMessageStore creates a new InMemoryMessageStore.
func NewInMemoryMessageStore() *InMemoryMessageStore {
	return &InMemoryMessageStore{
		store: make(map[string][]*message.ChatMessage),
	}
}

// Save stores messages.
func (s *InMemoryMessageStore) Save(ctx context.Context, threadID string, messages ...*message.ChatMessage) error {
	s.store[threadID] = messages
	return nil
}

// Load retrieves messages for a thread.
func (s *InMemoryMessageStore) Load(ctx context.Context, threadID string) ([]*message.ChatMessage, error) {
	messages, ok := s.store[threadID]
	if !ok {
		return []*message.ChatMessage{}, nil
	}
	return messages, nil
}

// Delete removes messages for a thread.
func (s *InMemoryMessageStore) Delete(ctx context.Context, threadID string) error {
	delete(s.store, threadID)
	return nil
}
