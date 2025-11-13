// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"encoding/json"

	"github.com/microsoft/agent-framework/go/message"
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
// A Thread is always constructed by an [Agent] so that the [Agent] can attach any necessary behaviors to the Thread.
// See the [Agent.NewThread] and [Agent.DeserializeThread] methods for more information.
//
// Because of these behaviors, a Thread may not be reusable across different agents, since each agent may add different
// behaviors to the Thread it creates.
//
// To support conversations that may need to survive application restarts or separate service requests,
// a Thread can be serialized and deserialized, so that it can be saved in a persistent store.
type Thread interface {
	json.Marshaler

	// AddMessage adds messages to the thread.
	AddMessage(ctx context.Context, messages ...*message.Message) error
}

type contextProviderThread interface {
	Thread

	ContextProvider() ContextProvider
}

var _ Thread = (*InMemoryThread)(nil)
var _ contextProviderThread = (*InMemoryThread)(nil)

// InMemoryThread provides an in-memory implementation of [Thread].
// Messages are stored entirely in local memory, providing fast access and manipulation capabilities.
type InMemoryThread struct {
	Messages []*message.Message

	wrappedContextProvider ContextProvider

	contextProvider inmemoryContextProvider
}

func NewInMemoryThread(cp ContextProvider) *InMemoryThread {
	return &InMemoryThread{
		wrappedContextProvider: cp,
	}
}

func (t *InMemoryThread) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Messages)
}

func (t *InMemoryThread) AddMessage(ctx context.Context, messages ...*message.Message) error {
	t.Messages = append(t.Messages, messages...)
	return nil
}

func (t *InMemoryThread) ContextProvider() ContextProvider {
	if t.contextProvider.messages == nil {
		t.contextProvider.messages = &t.Messages
		t.contextProvider.contextProvider = t.wrappedContextProvider
	}
	return &t.contextProvider
}

type inmemoryContextProvider struct {
	messages        *[]*message.Message
	contextProvider ContextProvider
}

func (p *inmemoryContextProvider) Invoking(ctx context.Context, messages []*message.Message) (*Context, error) {
	var ctxp *Context
	if p.contextProvider != nil {
		var err error
		ctxp, err = p.contextProvider.Invoking(ctx, messages)
		if err != nil {
			return nil, err
		}
	}
	if ctxp != nil {
		ctxp.Messages = append(ctxp.Messages, *p.messages...)
	} else {
		ctxp = &Context{
			Messages: *p.messages,
		}
	}
	return ctxp, nil
}

func (p *inmemoryContextProvider) Invoked(ctx context.Context, messages []*message.Message, responses []*message.Message, err error) error {
	if p.contextProvider != nil {
		return p.contextProvider.Invoked(ctx, messages, responses, err)
	}
	// Nothing to do for in-memory context, messages are already added via InMemoryThread.AddMessage.
	return nil
}
