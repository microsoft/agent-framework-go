// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/message"
)

// MessageStore defines a contract for storing and retrieving messages associated with an agent conversation.
type MessageStore interface {
	// Add adds messages to the store.
	// Messages should be added in the order they were generated to maintain proper chronological sequence.
	Add(ctx context.Context, msgs ...*message.Message) error

	// Messages retrieves all messages from the store that should be provided as context for the next agent invocation.
	All(ctx context.Context) iter.Seq2[*message.Message, error]
}

var _ MessageStore = (*InMemoryMessageStore)(nil)

// InMemoryMessageStore is an in-memory implementation of the MessageStore interface.
type InMemoryMessageStore struct {
	Messages []*message.Message
}

func (s *InMemoryMessageStore) Add(ctx context.Context, msgs ...*message.Message) error {
	s.Messages = append(s.Messages, msgs...)
	return nil
}

func (s *InMemoryMessageStore) All(ctx context.Context) iter.Seq2[*message.Message, error] {
	return func(yield func(*message.Message, error) bool) {
		for _, msg := range s.Messages {
			if !yield(msg, nil) {
				return
			}
		}
	}
}
