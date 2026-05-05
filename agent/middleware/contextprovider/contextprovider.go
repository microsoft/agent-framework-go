// Copyright (c) Microsoft. All rights reserved.

package contextprovider

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

// New adapts context providers into middleware for callers that explicitly need
// middleware composition. Agents configured with agent.Config.ContextProviders
// run providers through the agent lifecycle rather than through this adapter.
func New(providers ...*agent.ContextProvider) agent.Middleware {
	activeProviders := make([]*agent.ContextProvider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			activeProviders = append(activeProviders, provider)
		}
	}
	if len(activeProviders) == 0 {
		panic("at least one context provider is required")
	}
	return &contextProviderRunner{providers: activeProviders}
}

type contextProviderRunner struct {
	providers []*agent.ContextProvider
}

func (r *contextProviderRunner) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		options = slices.Clone(options)
		for _, provider := range r.providers {
			var err error
			messages, options, err = provider.BeforeRun(ctx, messages, options...)
			if err != nil {
				yield(nil, err)
				return
			}
		}

		requestMessages := slices.Clone(messages)
		var resp agent.Response
		for update, err := range next(ctx, messages, options...) {
			if update != nil {
				resp.Update(update)
			}
			if !yield(update, err) {
				break
			}
		}
		resp.Coalesce()

		for _, provider := range r.providers {
			if err := provider.AfterRun(ctx, slices.Clone(requestMessages), slices.Clone(resp.Messages), options...); err != nil {
				yield(nil, err)
				return
			}
		}
	}
}
