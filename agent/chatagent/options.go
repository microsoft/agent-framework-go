// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"log/slog"
	"slices"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/memory"
)

type Options struct {
	ID          string
	Name        string
	Description string

	Instructions string
	ChatOptions  *ChatOptions
	Logger       *slog.Logger

	DisableFuncAutoCall bool
	Middlewares         []middleware.Middleware

	NewMessageStore    func() memory.MessageStore
	NewContextProvider func() memory.ContextProvider
}

func (o *Options) Clone() *Options {
	if o == nil {
		return nil
	}
	clone := *o
	clone.Middlewares = slices.Clone(o.Middlewares)
	clone.ChatOptions = o.ChatOptions.Clone()
	return &clone
}

type opts struct {
	*ChatOptions
}

func (opts) RunOption() {}

func (o opts) Value() any {
	return o.ChatOptions
}

func WithOptions(options *ChatOptions) agentopt.RunOption {
	return opts{options}
}

type newClientOpts struct {
	NewClient func(chatclient.Client) chatclient.Client
}

func (newClientOpts) RunOption() {}

func (o newClientOpts) Value() any {
	return o.NewClient
}

func WithNewClient(newClient func(chatclient.Client) chatclient.Client) agentopt.RunOption {
	return newClientOpts{newClient}
}

type conversationIDOpt string

func (conversationIDOpt) NewThreadOption() {}

func (o conversationIDOpt) Value() any { return string(o) }

func WithConversationID(conversationID string) agentopt.NewThreadOption {
	return conversationIDOpt(conversationID)
}
