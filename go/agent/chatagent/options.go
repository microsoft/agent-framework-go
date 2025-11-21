// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"log/slog"

	"github.com/microsoft/agent-framework/go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework/go/memory"
)

type Options struct {
	ID          string
	Name        string
	Description string

	Instructions string
	ChatOptions  *ChatOptions
	Logger       *slog.Logger

	UseProvidedChatClientAsIs bool

	NewMessageStore    func() memory.MessageStore
	NewContextProvider func() memory.ContextProvider
}

func (o *Options) Clone() *Options {
	if o == nil {
		return nil
	}
	clone := *o
	clone.ChatOptions = o.ChatOptions.Clone()
	return &clone
}

// RunOptions contains options for agent execution.
type RunOptions struct {
	ChatOptions *ChatOptions

	NewClient func(chatclient.Client) chatclient.Client
}
