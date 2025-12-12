// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"log/slog"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework-go/memory"
)

type Options struct {
	ID          string
	Name        string
	Description string

	Instructions string
	ChatOptions  *ChatOptions
	Logger       *slog.Logger

	// If nil, a default middleware chain will be used.
	Middlewares []agent.Middleware

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

type opts struct {
	*ChatOptions
}

func (opts) AgentOption() {}

func (o opts) Value() any {
	return o.ChatOptions
}

func WithOptions(options *ChatOptions) agent.Option {
	return opts{options}
}

type newClientOpts struct {
	NewClient func(chatclient.Client) chatclient.Client
}

func (newClientOpts) AgentOption() {}

func (o newClientOpts) Value() any {
	return o.NewClient
}

func WithNewClient(newClient func(chatclient.Client) chatclient.Client) agent.Option {
	return newClientOpts{newClient}
}
