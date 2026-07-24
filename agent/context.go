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

// ContextProvider participates in an agent invocation lifecycle by supplying
// additional context before a run and processing context after a run completes.
//
// Context providers can retrieve relevant information, add instructions, inject
// contextual messages, provide tools for the current invocation, and persist or
// learn from request and response messages after successful runs.
//
// Prefer creating providers with [NewContextProvider]. Implement [ContextProvider]
// directly when a provider needs custom filtering, merging, source attribution,
// failure handling, or non-additive behavior such as compaction or truncation.
//
// # Security considerations
//
// Context providers may inject messages with any role, including system, which has
// the highest trust level and directly shapes LLM behavior. Developers must ensure
// that all providers attached to an agent are trusted. Agent Framework does not
// validate or filter the data returned by providers — it is accepted as-is and
// merged into the request context. If a provider retrieves data from an external
// source (e.g., a vector database or memory service), be aware that a compromised
// data source could introduce adversarial content designed to manipulate LLM behavior
// via indirect prompt injection. Implementers should validate and sanitize data
// retrieved from external sources before returning it.
type ContextProvider interface {
	// Invoking returns the input messages and options with provider-specific additions applied.
	Invoking(context.Context, InvokingContext) ([]*message.Message, []Option, error)

	// Invoked persists context-related state after an agent invocation finishes.
	Invoked(context.Context, InvokedContext) error
}

// InvokingContext contains the agent invocation context available to a
// [ContextProvider] before the provider run starts.
type InvokingContext struct {
	// Messages are the request messages currently being prepared for the invocation.
	Messages []*message.Message

	// Options are the run options currently being prepared for the invocation.
	Options []Option
}

// InvokedContext contains the agent invocation context available to a
// [ContextProvider] after a provider run completes.
type InvokedContext struct {
	// RequestMessages are the messages used for the invocation.
	RequestMessages []*message.Message

	// ResponseMessages are the messages produced by the invocation.
	ResponseMessages []*message.Message

	// Options are the run options used for the invocation.
	Options []Option

	// Err is the error returned by the invocation, if any.
	Err error
}

// ContextProviderConfig configures the provider created by [NewContextProvider].
type ContextProviderConfig struct {
	// Unique identifier for this provider instance (required).
	SourceID string

	// Optional filter applied to request messages before Provide.
	// Defaults to [messagefilter.ExternalOnly].
	ProvideInputMessageFilter messagefilter.Filter

	// Optional filter applied to request messages before Store.
	// Defaults to [messagefilter.ExternalOnly].
	StoreInputRequestMessageFilter messagefilter.Filter

	// Optional filter applied to response messages before Store.
	// Defaults to passing all response messages through.
	StoreInputResponseMessageFilter messagefilter.Filter

	// Optional retrieval hook that returns additional provider context messages and run options.
	Provide func(context.Context, InvokingContext) ([]*message.Message, []Option, error)

	// Optional storage hook. Defaults to no-op.
	Store func(context.Context, InvokedContext) error
}

type defaultContextProvider struct {
	config ContextProviderConfig
}

// NewContextProvider creates the default additive context provider.
//
// The provider filters input messages before invoking Provide, treats Provide
// results as additive, source-stamps provided messages, appends provided
// messages and options to the original invocation context, filters stored
// request and response messages, and skips Store when the run fails.
func NewContextProvider(config ContextProviderConfig) ContextProvider {
	return &defaultContextProvider{config: config}
}

// Invoking returns the input messages and options with this provider's additions applied.
func (p *defaultContextProvider) Invoking(ctx context.Context, invoking InvokingContext) ([]*message.Message, []Option, error) {
	if p.config.SourceID == "" {
		panic("SourceID is required")
	}
	if p.config.Provide == nil {
		return invoking.Messages, invoking.Options, nil
	}

	provideFilter := p.config.ProvideInputMessageFilter
	if provideFilter == nil {
		provideFilter = messagefilter.ExternalOnly
	}
	provideMessages, err := provideFilter(ctx, slices.Clone(invoking.Messages))
	if err != nil {
		return nil, nil, err
	}

	providedMessages, providedOptions, err := p.config.Provide(ctx, InvokingContext{Messages: provideMessages, Options: invoking.Options})
	if err != nil {
		return nil, nil, err
	}

	outMessages := invoking.Messages
	if len(providedMessages) > 0 {
		source := message.Source{Type: SourceTypeContextProvider, ID: p.config.SourceID}
		for i, msg := range providedMessages {
			if msg == nil || msg.Source == source {
				continue
			}
			marked := msg.Clone()
			marked.Source = source
			providedMessages[i] = marked
		}
		outMessages = append(outMessages, providedMessages...)
	}

	outOptions := invoking.Options
	if len(providedOptions) > 0 {
		outOptions = append(outOptions, providedOptions...)
	}

	return outMessages, outOptions, nil
}

// Invoked persists context-related state after an agent invocation finishes.
func (p *defaultContextProvider) Invoked(ctx context.Context, invoked InvokedContext) error {
	if p.config.SourceID == "" {
		panic("SourceID is required")
	}
	if invoked.Err != nil {
		return nil
	}
	if p.config.Store == nil {
		return nil
	}
	requestFilter := p.config.StoreInputRequestMessageFilter
	if requestFilter == nil {
		requestFilter = messagefilter.ExternalOnly
	}
	responseFilter := p.config.StoreInputResponseMessageFilter
	if responseFilter == nil {
		responseFilter = messagefilter.PassThrough
	}
	filteredReq, err := requestFilter(ctx, invoked.RequestMessages)
	if err != nil {
		return err
	}
	filteredResp, err := responseFilter(ctx, invoked.ResponseMessages)
	if err != nil {
		return err
	}
	return p.config.Store(ctx, InvokedContext{RequestMessages: filteredReq, ResponseMessages: filteredResp, Options: invoked.Options, Err: invoked.Err})
}

// ContextProviderMiddleware adapts a context provider into middleware for
// callers that explicitly need middleware composition. Agents configured with
// Config.ContextProviders run providers through the agent lifecycle rather than
// through this adapter.
func ContextProviderMiddleware(p ContextProvider) Middleware {
	if p == nil {
		panic("context provider is required")
	}
	return &contextProviderMiddleware{provider: p}
}

type contextProviderMiddleware struct {
	provider ContextProvider
}

func (r *contextProviderMiddleware) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return func(yield func(*ResponseUpdate, error) bool) {
		options = slices.Clone(options)
		var err error
		messages, options, err = r.provider.Invoking(ctx, InvokingContext{Messages: messages, Options: options})
		if err != nil {
			yield(nil, err)
			return
		}

		requestMessages := slices.Clone(messages)
		var resp Response
		var invokeErr error
		var stopped bool
		for update, err := range next(ctx, messages, options...) {
			if update != nil {
				resp.Update(update)
			}
			if err != nil {
				invokeErr = err
			}
			if !yield(update, err) {
				stopped = true
				break
			}
		}
		resp.Coalesce()

		if err := r.provider.Invoked(ctx, InvokedContext{RequestMessages: requestMessages, ResponseMessages: resp.Messages, Options: options, Err: invokeErr}); err != nil {
			if !stopped {
				yield(nil, err)
			}
			return
		}
	}
}
