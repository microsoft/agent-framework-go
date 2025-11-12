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
	Instructions string
	Messages     []*Message
	Tools        []tool.Tool
}

type ContextProvider interface {
	Invoking(ctx context.Context, messages []*Message) (*Context, error)
	Invoked(ctx context.Context, messages []*Message, responses []*Message, err error) error
}
