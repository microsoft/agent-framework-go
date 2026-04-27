// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

// ContextProvider provides an extensible implementation for injecting and persisting
// additional context messages around an agent invocation.
type ContextProvider struct {
	// Unique identifier for this provider instance (required).
	SourceID string

	// Optional filter applied to request messages before Store.
	// Defaults to excluding messages from the same provider SourceID.
	StoreRequestFilter messagefilter.Filter

	// Optional filter applied to response messages before Store.
	// Defaults to passing all response messages through.
	StoreResponseFilter messagefilter.Filter

	// Optional retrieval hook that returns updated provider context messages and run options.
	// Returned slices should include the original messages and options with any additions appended.
	// Defaults to returning the original messages and options unchanged.
	Provide func(context.Context, []*message.Message, ...Option) ([]*message.Message, []Option, error)

	// Optional storage hook. Defaults to no-op.
	Store func(context.Context, []*message.Message, []*message.Message, ...Option) error
}

// BeforeRun returns the input messages and options with this provider's additions applied.
func (p *ContextProvider) BeforeRun(ctx context.Context, messages []*message.Message, options ...Option) ([]*message.Message, []Option, error) {
	if p.SourceID == "" {
		panic("SourceID is required")
	}
	if p.Provide == nil {
		return messages, options, nil
	}

	outMessages, outOptions, err := p.Provide(ctx, messages, options...)
	if err != nil {
		return nil, nil, err
	}

	if outMessages == nil {
		outMessages = messages
	}
	if messageSliceExtends(outMessages, messages) {
		outMessages = slices.Clone(outMessages)
		for i := len(messages); i < len(outMessages); i++ {
			outMessages[i] = p.withAgentRequestMessageSource(outMessages[i])
		}
	}

	if outOptions == nil {
		outOptions = options
	}

	return outMessages, outOptions, nil
}

func messageSliceExtends(out, in []*message.Message) bool {
	if len(out) < len(in) {
		return false
	}
	for i := range in {
		if out[i] != in[i] {
			return false
		}
	}
	return true
}

// AfterRun persists context-related state after an agent invocation finishes.
func (p *ContextProvider) AfterRun(ctx context.Context, requestMessages, responseMessages []*message.Message, options ...Option) error {
	if p.SourceID == "" {
		panic("SourceID is required")
	}
	if p.Store == nil {
		return nil
	}
	requestFilter := p.StoreRequestFilter
	if requestFilter == nil {
		requestFilter = messagefilter.NotSources(p.SourceID)
	}
	filteredRequestMessages, err := requestFilter(ctx, requestMessages)
	if err != nil {
		return err
	}

	responseFilter := p.StoreResponseFilter
	if responseFilter == nil {
		responseFilter = messagefilter.PassThrough
	}
	filteredResponseMessages, err := responseFilter(ctx, responseMessages)
	if err != nil {
		return err
	}

	return p.Store(ctx, filteredRequestMessages, filteredResponseMessages, options...)
}

func (p *ContextProvider) withAgentRequestMessageSource(msg *message.Message) *message.Message {
	if msg == nil {
		return nil
	}
	if msg.SourceID == p.SourceID {
		return msg
	}
	out := msg.Clone()
	out.SourceID = p.SourceID
	return out
}

// newContextProviderMiddleware returns a middleware that invokes the provided context providers in order
// before the wrapped run and persists them in reverse order after the run.
func newContextProviderMiddleware(providers ...*ContextProvider) Middleware {
	activeProviders := make([]*ContextProvider, 0, len(providers))
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
	providers []*ContextProvider
}

func (r *contextProviderRunner) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*message.ResponseUpdate, error] {
	session, _ := GetOption(options, WithSession)

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
			if update != nil && (session == nil || session.ServiceID == "") {
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
