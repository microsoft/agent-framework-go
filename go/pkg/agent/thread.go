// Copyright (c) Microsoft. All rights reserved.

package agent

import "github.com/microsoft/agent-framework/go/pkg/message"

// Thread represents a conversation thread that maintains message history.
type Thread interface {
	// ID returns the unique identifier.
	ID() string

	// AddMessage adds a message to the thread.
	AddMessage(message *message.ChatMessage)

	// GetMessages returns all messages in the thread.
	GetMessages() []*message.ChatMessage

	// Clear removes all messages from the thread.
	Clear()

	// Serialize serializes the thread to JSON.
	Serialize() ([]byte, error)
}
