// Copyright (c) Microsoft. All rights reserved.

package contextprovider

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

// New returns a middleware that invokes the provided context providers in order
// before the wrapped run and persists them in reverse order after the run.
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

func (r *contextProviderRunner) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
	session, _ := agent.GetOption(options, agent.WithSession)

	return func(yield func(*message.ResponseUpdate, error) bool) {
		options = slices.Clone(options)
		for _, provider := range r.providers {
			var err error
			messages, options, err = provider.BeforeRun(ctx, messages, options...)
			if err != nil {
				yield(nil, err)
				return
			}
		}
		var resp message.Response
		for update, err := range next(ctx, messages, options...) {
			if update != nil && (session == nil || session.ServiceID() == "") {
				resp.Update(update)
			}
			if !yield(update, err) {
				break
			}
		}
		resp.Coalesce()
		requestMessages := slices.Clone(messages)
		responseMessages := slices.Clone(resp.Messages)

		for _, provider := range slices.Backward(r.providers) {
			if err := provider.AfterRun(ctx, requestMessages, responseMessages, options...); err != nil {
				yield(nil, err)
				return
			}
		}
	}
}
