// Copyright (c) Microsoft. All rights reserved.

package contextprovider

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
)

// New returns a middleware that invokes the provided context providers in order
// before the wrapped run and persists them in reverse order after the run.
func New(providers ...*memory.ContextProvider) middleware.Middleware {
	activeProviders := make([]*memory.ContextProvider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			activeProviders = append(activeProviders, provider)
		}
	}
	if len(activeProviders) == 0 {
		panic("at least one context provider is required")
	}
	return &runner{providers: activeProviders}
}

type runner struct {
	providers []*memory.ContextProvider
}

func (r *runner) Run(next middleware.RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	session, _ := agentopt.Get(options, agentopt.Session)

	return func(yield func(*message.ResponseUpdate, error) bool) {
		currentMessages := messages
		currentTools := slices.Collect(agentopt.All(options, agentopt.Tool))
		runOptions := options
		for _, provider := range r.providers {
			providerContext, err := provider.BeforeRun(memory.BeforeRunContext{
				Context:  ctx,
				Session:  session,
				Messages: currentMessages,
				Tools:    currentTools,
			})
			if err != nil {
				yield(nil, err)
				return
			}
			if len(providerContext.Messages) > 0 {
				merged := make([]*message.Message, 0, len(providerContext.Messages)+len(currentMessages))
				merged = append(merged, providerContext.Messages...)
				merged = append(merged, currentMessages...)
				currentMessages = merged
			}
			if len(providerContext.Tools) > 0 {
				currentTools = append(currentTools, providerContext.Tools...)
				for _, tool := range providerContext.Tools {
					runOptions = append(runOptions, agentopt.Tool(tool))
				}
			}
		}

		var resp message.Response
		var runErr error
		for update, err := range next(ctx, currentMessages, runOptions...) {
			if update != nil && session.ServiceID == "" {
				resp.Update(update)
			}
			if !yield(update, err) {
				if err != nil {
					runErr = err
				}
				break
			}
		}
		resp.Coalesce()

		for i := len(r.providers) - 1; i >= 0; i-- {
			provider := r.providers[i]
			if err := provider.AfterRun(memory.AfterRunContext{
				Context:          ctx,
				Session:          session,
				RequestMessages:  slices.Clone(currentMessages),
				ResponseMessages: slices.Clone(resp.Messages),
				Tools:            currentTools,
				InvokeError:      runErr,
			}); err != nil {
				yield(nil, err)
				return
			}
		}
	}
}
