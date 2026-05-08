// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

// ContextProvider provides a structured subset of middleware behavior for
// injecting and persisting additional context around an agent invocation.
//
// A provider can add, replace, or annotate request messages, add run options,
// and persist filtered request and response messages after the run completes.
// It does not wrap the provider [RunFunc] directly, so it cannot intercept
// individual streamed updates, emit replacement updates, retry or replace the
// provider invocation, or transform provider errors as they occur.
// Use [Middleware] for those lower-level run-pipeline behaviors.
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
	// Messages that are not pointer-identical to the original input messages are marked with SourceID.
	// Defaults to returning the original messages and options unchanged.
	Provide func(context.Context, []*message.Message, ...Option) ([]*message.Message, []Option, error)

	// Optional storage hook. Defaults to no-op.
	Store func(context.Context, []*message.Message, []*message.Message, ...Option) error
}

// Middleware adapts this context provider into middleware for callers that
// explicitly need middleware composition. Agents configured with
// Config.ContextProviders run providers through the agent lifecycle rather than
// through this adapter.
func (p *ContextProvider) Middleware() Middleware {
	if p == nil {
		panic("context provider is required")
	}
	return &contextProviderMiddleware{provider: p}
}

type contextProviderMiddleware struct {
	provider *ContextProvider
}

func (r *contextProviderMiddleware) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return func(yield func(*ResponseUpdate, error) bool) {
		options = slices.Clone(options)
		var err error
		messages, options, err = r.provider.BeforeRun(ctx, messages, options...)
		if err != nil {
			yield(nil, err)
			return
		}

		requestMessages := slices.Clone(messages)
		var resp Response
		for update, err := range next(ctx, messages, options...) {
			if update != nil {
				resp.Update(update)
			}
			if !yield(update, err) {
				break
			}
		}
		resp.Coalesce()

		if err := r.provider.AfterRun(ctx, requestMessages, resp.Messages, options...); err != nil {
			yield(nil, err)
			return
		}
	}
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
	outMessages = p.withProviderSource(outMessages, messages)

	if outOptions == nil {
		outOptions = options
	}

	return outMessages, outOptions, nil
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

func (p *ContextProvider) withProviderSource(outMessages, inMessages []*message.Message) []*message.Message {
	if len(outMessages) == 0 {
		return outMessages
	}
	originals := make(map[*message.Message]struct{}, len(inMessages))
	for _, msg := range inMessages {
		originals[msg] = struct{}{}
	}

	var cloned []*message.Message
	for i, msg := range outMessages {
		if _, ok := originals[msg]; ok {
			continue
		}
		marked := p.withAgentRequestMessageSource(msg)
		if marked == msg {
			continue
		}
		if cloned == nil {
			cloned = slices.Clone(outMessages)
		}
		cloned[i] = marked
	}
	if cloned != nil {
		return cloned
	}
	return outMessages
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
