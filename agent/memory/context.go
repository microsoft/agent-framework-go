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
//
// It provides context about the invocation before the underlying AI model is invoked,
// including the accumulated [Context] being built by the provider pipeline and the
// input messages from the caller.
type InvokingContext struct {
	context.Context

	// AccContext is the accumulated context being built for the current invocation.
	// If multiple [ContextProvider] instances are used, each provider will receive
	// the context returned by the previous provider, allowing them to build on top
	// of each other's context.
	AccContext *Context

	// Messages contains the input messages from the caller for this invocation.
	Messages []*message.Message

	// Session is the agent session associated with this invocation, or nil if none.
	Session Session
}

// InvokedContext contains the context information provided to a [ContextProvider.Invoked] call.
//
// It provides context about a completed agent invocation, including the request
// messages that were sent for this invocation and the response messages that were
// generated. It also indicates whether the invocation succeeded or failed.
type InvokedContext struct {
	context.Context

	// RequestMessages contains the request messages that were used by the agent for
	// this invocation (for example, the user's input for this turn and any additional
	// messages provided by context providers for this turn). Previously stored history
	// is not required to be included here and may be managed separately (e.g., by a
	// message history provider).
	RequestMessages []*message.Message

	// ResponseMessages contains the response messages generated during this invocation,
	// or nil if the invocation failed.
	ResponseMessages []*message.Message

	// InvokeError contains the error that caused the invocation to fail,
	// or nil if the invocation succeeded.
	InvokeError error

	// Session is the agent session associated with this invocation, or nil if none.
	Session Session
}

// ContextProvider defines a contract for components that enhance AI context management during agent invocations.
//
// A context provider participates in the agent invocation lifecycle by:
//   - Listening to changes in conversations
//   - Providing additional context to agents during invocation
//   - Supplying additional function tools for enhanced capabilities
//   - Processing invocation results for state management or learning
//
// Context providers operate through a two-phase lifecycle: they are called at the start of invocation via
// [ContextProvider.Invoking] and at the end via [ContextProvider.Invoked].
type ContextProvider interface {
	// Invoking is called before agent invocation. It returns additional context to be used
	// during the invocation, or an error if context retrieval fails.
	// The returned *Context may be nil even if there is no error and no additional context is provided.
	Invoking(*InvokingContext) (*Context, error)

	// Invoked is called after the model invocation to process the results, even if the
	// model invocation itself failed. It is not called if [ContextProvider.Invoking] returns
	// an error (since the model was never invoked in that case).
	// Implementations should check [InvokedContext.InvokeError] to determine
	// whether the invocation was successful.
	Invoked(*InvokedContext) error
}
