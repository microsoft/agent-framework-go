// Copyright (c) Microsoft. All rights reserved.

package inmemory

import (
	"context"

	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
)

var _ memory.Thread = (*Thread)(nil)
var _ memory.ContextProviderThread = (*Thread)(nil)

// Thread provides an in-memory implementation of [memory.Thread].
// Messages are stored entirely in local memory, providing fast access and manipulation capabilities.
type Thread struct {
	Messages []*message.Message
	Provider memory.ContextProvider `json:"ContextProvider"`

	contextProvider contextProvider
}

func (t *Thread) AddMessage(ctx context.Context, messages ...*message.Message) error {
	t.Messages = append(t.Messages, messages...)
	return nil
}

func (t *Thread) ContextProvider() memory.ContextProvider {
	if t.contextProvider.Messages == nil {
		t.contextProvider.Messages = &t.Messages
		t.contextProvider.ContextProvider = t.Provider
	}
	return &t.contextProvider
}

type contextProvider struct {
	Messages        *[]*message.Message
	ContextProvider memory.ContextProvider `json:",omitempty"`
}

func (p *contextProvider) Invoking(ctx *memory.InvokingContext) (*memory.Context, error) {
	var ctxp *memory.Context
	if p.ContextProvider != nil {
		var err error
		ctxp, err = p.ContextProvider.Invoking(ctx)
		if err != nil {
			return nil, err
		}
	}
	if ctxp != nil {
		ctxp.Messages = append(ctxp.Messages, *p.Messages...)
	} else {
		ctxp = &memory.Context{
			Messages: *p.Messages,
		}
	}
	return ctxp, nil
}

func (p *contextProvider) Invoked(ctx *memory.InvokedContext) error {
	if p.ContextProvider != nil {
		return p.ContextProvider.Invoked(ctx)
	}
	// Nothing to do for in-memory context, messages are already added via InMemoryThread.AddMessage.
	return nil
}
