// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

// SourceTypeContextProvider represents a message that originated from a context provider.
const SourceTypeContextProvider message.SourceType = "context-provider"

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
	// Defaults to [messagefilter.ExternalOnly].
	StoreRequestFilter messagefilter.Filter

	// Optional filter applied to response messages before Store.
	// Defaults to passing all response messages through.
	StoreResponseFilter messagefilter.Filter

	// Optional retrieval hook that returns updated provider context messages and run options.
	// Messages that are not pointer-identical to the messages passed to Provide are marked with this provider's source.
	// If unset, the original messages and options are returned unchanged.
	// If set, returned options are used as-is; returned messages are source-stamped as described above.
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
		var runErr error
		for update, err := range next(ctx, messages, options...) {
			if update != nil {
				resp.Update(update)
			}
			if err != nil {
				runErr = err
			}
			if !yield(update, err) {
				break
			}
		}
		resp.Coalesce()
		if runErr != nil {
			return
		}

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

	inputMessages := slices.Clone(messages)
	messages, options, err := p.Provide(ctx, messages, options...)
	if err != nil {
		return nil, nil, err
	}

	markNewMessagesWithSource(messages, inputMessages, message.Source{Type: SourceTypeContextProvider, ID: p.SourceID}, true)
	return messages, options, nil
}

// AfterRun persists context-related state after an agent invocation finishes.
func (p *ContextProvider) AfterRun(ctx context.Context, requestMessages, responseMessages []*message.Message, options ...Option) error {
	if p.SourceID == "" {
		panic("SourceID is required")
	}
	requestFilter := p.StoreRequestFilter
	if requestFilter == nil {
		requestFilter = messagefilter.ExternalOnly
	}
	return runStoreHook(ctx, p.Store, requestFilter, p.StoreResponseFilter, requestMessages, responseMessages, options)
}

// markNewMessagesWithSource marks messages in outMessages that are not pointer-identical
// to messages in inMessages with source. Marked messages are cloned before source is set.
// When overwrite is false, messages that already have any source are preserved as-is.
// Messages that are nil, already have source, or are present in inMessages are skipped.
func markNewMessagesWithSource(outMessages, inMessages []*message.Message, source message.Source, overwrite bool) {
	originals := make(map[*message.Message]struct{}, len(inMessages))
	for _, msg := range inMessages {
		originals[msg] = struct{}{}
	}
	if len(outMessages) == 0 {
		return
	}
	for i, msg := range outMessages {
		if _, ok := originals[msg]; ok {
			continue
		}
		if msg == nil || msg.Source == source || !overwrite && msg.Source != (message.Source{}) {
			continue
		}
		marked := msg.Clone()
		marked.Source = source
		outMessages[i] = marked
	}
}

// runStoreHook applies request and response filters then calls store.
// When store is nil, it is a no-op. A nil requestFilter defaults to
// messagefilter.PassThrough; a nil responseFilter defaults to
// messagefilter.PassThrough.
func runStoreHook(
	ctx context.Context,
	store func(context.Context, []*message.Message, []*message.Message, ...Option) error,
	requestFilter, responseFilter messagefilter.Filter,
	requestMessages, responseMessages []*message.Message,
	options []Option,
) error {
	if store == nil {
		return nil
	}
	if requestFilter == nil {
		requestFilter = messagefilter.PassThrough
	}
	if responseFilter == nil {
		responseFilter = messagefilter.PassThrough
	}
	filteredReq, err := requestFilter(ctx, requestMessages)
	if err != nil {
		return err
	}
	filteredResp, err := responseFilter(ctx, responseMessages)
	if err != nil {
		return err
	}
	return store(ctx, filteredReq, filteredResp, options...)
}
