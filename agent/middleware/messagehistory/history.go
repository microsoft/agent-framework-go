// Copyright (c) Microsoft. All rights reserved.

package messagehistory

import (
	"context"
	"fmt"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/message"
)

const stateBagKey = "messagehistory.inmemory.messages"

// inmemory is an in-memory middleware that prepends historical
// messages and appends new request/response messages after successful runs.
type inmemory struct {
}

func New() middleware.Middleware {
	return inmemory{}
}

func (s inmemory) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	session, _ := agentopt.Get(opts, agentopt.Session)
	if session == nil {
		return next(ctx, messages, opts...)
	}
	// Prepend historical messages from session state bag.
	stateBag := session.GetStateBag()
	var history []*message.Message
	if ok, err := stateBag.Get(stateBagKey, &history); err == nil && ok && len(history) > 0 {
		messages = append(slices.Clone(history), messages...)
	} else if err != nil {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, fmt.Errorf("failed to load message history from session state bag: %w", err))
		}
	}
	return func(yield func(*message.ResponseUpdate, error) bool) {
		var resp message.Response
		for update, err := range next(ctx, messages, opts...) {
			if err != nil {
				yield(nil, err)
				return
			}
			resp.Update(update)
			if !yield(update, nil) {
				return
			}
		}
		resp.Coalesce()
		// Append new request/response messages to session state bag.
		updated := append(messages, resp.Messages...)
		stateBag.Set(stateBagKey, updated)
	}
}
