// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
)

// Thread contains the state of a specific conversation with an agent which may include:
//
//   - Conversation history or a reference to externally stored conversation history.
//   - Memories or a reference to externally stored memories.
//   - Any other state that the agent needs to persist across runs for a conversation.
//
// A Thread may also have behaviors attached to it that may include:
//
//   - Customized storage of state.
//   - Data extraction from and injection into a conversation.
//   - Chat history reduction, e.g. where messages needs to be summarized or truncated to reduce the size.
//
// A Thread is always constructed by an [agent.Agent] so that the [agent.Agent] can attach any necessary behaviors to the Thread.
// See the [agent.Agent.NewThread] and [agent.Agent.UnmarshalThread] methods for more information.
//
// Because of these behaviors, a Thread may not be reusable across different agents, since each agent may add different
// behaviors to the Thread it creates.
//
// To support conversations that may need to survive application restarts or separate service requests,
// a Thread can be serialized and deserialized, so that it can be saved in a persistent store.
type Thread interface {
	// MessagesReceived adds messages to the thread.
	MessagesReceived(ctx context.Context, messages ...*message.Message) error
}

// ContextProviderThread is a Thread that can provide its own context.
type ContextProviderThread interface {
	Thread

	ContextProvider() ContextProvider
}

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

// MessageStore defines a contract for storing and retrieving messages associated with an agent conversation.
type MessageStore interface {
	// Add adds messages to the store.
	// Messages should be added in the order they were generated to maintain proper chronological sequence.
	Add(ctx context.Context, msgs ...*message.Message) error

	// Messages retrieves all messages from the store that should be provided as context for the next agent invocation.
	All(ctx context.Context) iter.Seq2[*message.Message, error]
}
