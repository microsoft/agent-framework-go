// Copyright (c) Microsoft. All rights reserved.

package thread

import (
	"github.com/google/uuid"
	"github.com/microsoft/agent-framework/go/pkg/message"
)

// InMemoryThread is a simple in-memory implementation of [agent.Thread].
type InMemoryThread struct {
	id       string
	messages []*message.ChatMessage
}

// NewInMemoryThread creates a new InMemoryThread.
func NewInMemoryThread() *InMemoryThread {
	return &InMemoryThread{
		id:       uuid.New().String(),
		messages: make([]*message.ChatMessage, 0),
	}
}

// ID returns the thread's unique identifier.
func (t *InMemoryThread) ID() string {
	return t.id
}

// AddMessage adds a message to the thread.
func (t *InMemoryThread) AddMessage(msg *message.ChatMessage) {
	t.messages = append(t.messages, msg)
}

// GetMessages returns all messages in the thread.
func (t *InMemoryThread) GetMessages() []*message.ChatMessage {
	return t.messages
}

// Clear removes all messages from the thread.
func (t *InMemoryThread) Clear() {
	t.messages = make([]*message.ChatMessage, 0)
}

// Serialize serializes the thread to JSON.
func (t *InMemoryThread) Serialize() ([]byte, error) {
	// TODO: Implement JSON serialization
	return []byte("{}"), nil
}
