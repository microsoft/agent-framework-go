// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
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
	Messages []*message.Message
	// Tools provides additional tools to make available during the invocation.
	Tools []tool.Tool
}

// InvokingContext contains the context information provided to a [ContextProvider.Invoking] call.
type InvokingContext struct {
	context.Context

	Messages []*message.Message
}

// InvokedContext contains the context information provided to a [ContextProvider.Invoked] call.
type InvokedContext struct {
	context.Context

	RequestMessages         []*message.Message
	ContextProviderMessages []*message.Message
	ResponsesMessages       []*message.Message
	Error                   error
}

// ContextProvider defines a contract for components that enhance AI context management during agent invocations.
type ContextProvider interface {
	// Invoking is called before agent invocation. It returns additional context to be used
	// during the invocation, or an error if context retrieval fails.
	// The returned *Context may be nil even if there is no error and no additional context is provided.
	Invoking(*InvokingContext) (*Context, error)

	// Invoked is called immediately after an agent has been invoked to process the results.
	Invoked(*InvokedContext) error
}
