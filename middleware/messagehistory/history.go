// Copyright (c) Microsoft. All rights reserved.

package messagehistory

import (
	"context"
	"fmt"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
)

const sessionKey = "messagehistory.inmemory.messages"

// inmemory is an in-memory middleware that prepends historical
// messages and appends new request/response messages after successful runs.
type inmemory struct {
}

func New() middleware.Middleware {
	return inmemory{}
}

func (s inmemory) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	session, _ := agentopt.Get(opts, agentopt.Session)
	if session == nil {
		return next(ctx, messages, opts...)
	}
	// Prepend historical messages from session state.
	var history []*message.Message
	if ok, err := session.Get(sessionKey, &history); err == nil && ok && len(history) > 0 {
		messages = append(slices.Clone(history), messages...)
	} else if err != nil {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, fmt.Errorf("failed to load message history from session state: %w", err))
		}
	}
	return func(yield func(*message.ResponseUpdate, error) bool) {
		var resp message.Response
		for update, err := range next(ctx, messages, opts...) {
			if err != nil {
				yield(nil, err)
				break
			}
			resp.Update(update)
			if !yield(update, nil) {
				return
			}
		}
		resp.Coalesce()
		// Append new request/response messages to session state.
		updated := append(messages, resp.Messages...)
		session.Set(sessionKey, updated)
	}
}
