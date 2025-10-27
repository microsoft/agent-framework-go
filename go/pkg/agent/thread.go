// Copyright (c) Microsoft. All rights reserved.

package agent

import "github.com/google/uuid"

// Thread represents a conversation thread that maintains message history.
type Thread interface {
	// ID returns the unique identifier.
	ID() string

	// AddMessage adds a message to the thread.
	AddMessage(message *Message)

	// GetMessages returns all messages in the thread.
	GetMessages() []*Message

	// Clear removes all messages from the thread.
	Clear()

	// Serialize serializes the thread to JSON.
	Serialize() ([]byte, error)
}

// InMemoryThread is a simple in-memory implementation of [Thread].
type InMemoryThread struct {
	id       string
	messages []*Message
}

// NewInMemoryThread creates a new InMemoryThread.
func NewInMemoryThread() *InMemoryThread {
	return &InMemoryThread{
		id:       uuid.New().String(),
		messages: make([]*Message, 0),
	}
}

// ID returns the thread's unique identifier.
func (t *InMemoryThread) ID() string {
	return t.id
}

// AddMessage adds a message to the thread.
func (t *InMemoryThread) AddMessage(msg *Message) {
	t.messages = append(t.messages, msg)
}

// GetMessages returns all messages in the thread.
func (t *InMemoryThread) GetMessages() []*Message {
	return t.messages
}

// Clear removes all messages from the thread.
func (t *InMemoryThread) Clear() {
	t.messages = make([]*Message, 0)
}

// Serialize serializes the thread to JSON.
func (t *InMemoryThread) Serialize() ([]byte, error) {
	// TODO: Implement JSON serialization
	return []byte("{}"), nil
}
