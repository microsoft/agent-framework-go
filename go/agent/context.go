// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"

	"github.com/microsoft/agent-framework/go/tool"
)

// Context represents additional context information that can be dynamically
// provided to AI models during agent invocations.
//
// Context serves as a container for contextual information that [ContextProvider] instances
// can supply to enhance AI model interactions. This context is combined across multiple providers
// and merged with the agent's base configuration before being passed to the underlying AI model.
type Context struct {
	// Instructions provides additional system instructions to be prepended to the conversation.
	Instructions string
	// Messages contains historical or contextual messages to be included in the conversation.
	Messages     []*Message
	// Tools provides additional tools to make available during the invocation.
	Tools        []tool.Tool
}

type ContextProvider interface {
	// Invoking is called before agent invocation. It returns additional context to be used
	// during the invocation, or an error if context retrieval fails.
	// The returned *Context must not be nil if err is nil.
	Invoking(ctx context.Context, messages []*Message) (*Context, error)
	Invoked(ctx context.Context, messages []*Message, responses []*Message, err error) error
}
