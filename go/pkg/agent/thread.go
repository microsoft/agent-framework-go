// Copyright (c) Microsoft. All rights reserved.

package agent

import "github.com/google/uuid"

// Thread represents a conversation thread that maintains message history.
type Thread[M any] interface {
	// ID returns the unique identifier.
	ID() string

	// AddMessage adds a message to the thread.
	AddMessage(message M)

	// GetMessages returns all messages in the thread.
	GetMessages() []M

	// Clear removes all messages from the thread.
	Clear()

	// Serialize serializes the thread to JSON.
	Serialize() ([]byte, error)
}

// InMemoryThread is a simple in-memory implementation of [Thread].
type InMemoryThread[M any] struct {
	id       string
	messages []M
}

// NewInMemoryThread creates a new InMemoryThread.
func NewInMemoryThread[M any]() *InMemoryThread[M] {
	return &InMemoryThread[M]{
		id:       uuid.New().String(),
		messages: make([]M, 0),
	}
}

// ID returns the thread's unique identifier.
func (t *InMemoryThread[M]) ID() string {
	return t.id
}

// AddMessage adds a message to the thread.
func (t *InMemoryThread[M]) AddMessage(msg M) {
	t.messages = append(t.messages, msg)
}

// GetMessages returns all messages in the thread.
func (t *InMemoryThread[M]) GetMessages() []M {
	return t.messages
}

// Clear removes all messages from the thread.
func (t *InMemoryThread[M]) Clear() {
	t.messages = make([]M, 0)
}

// Serialize serializes the thread to JSON.
func (t *InMemoryThread[M]) Serialize() ([]byte, error) {
	// TODO: Implement JSON serialization
	return []byte("{}"), nil
}
