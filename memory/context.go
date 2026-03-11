// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/microsoft/agent-framework-go/tool"
)

// Context represents invocation context that can be augmented by ContextProvider.
type Context struct {
	Messages []*message.Message
	Tools    []tool.Tool
}

// BeforeRunContext represents the parameters passed to ContextProvider.BeforeRun.
type BeforeRunContext struct {
	context.Context
	Session  *Session
	Messages []*message.Message
	Tools    []tool.Tool
}

// AfterRunContext represents the parameters passed to ContextProvider.AfterRun.
type AfterRunContext struct {
	context.Context
	Session          *Session
	RequestMessages  []*message.Message
	ResponseMessages []*message.Message
	Tools            []tool.Tool
	InvokeError      error
}

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

	// Optional retrieval hook that returns additional provider context.
	// Defaults to returning no additional context.
	Provide func(BeforeRunContext) (Context, error)

	// Optional storage hook. Defaults to no-op.
	Store func(AfterRunContext) error
}

// BeforeRun returns only the additional Context contributed by this provider.
func (p *ContextProvider) BeforeRun(ctx BeforeRunContext) (Context, error) {
	if p.SourceID == "" {
		panic("SourceID is required")
	}
	if p.Provide == nil {
		return Context{}, nil
	}

	provided, err := p.Provide(ctx)
	if err != nil {
		return Context{}, err
	}

	var stampedProvidedMessages []*message.Message
	if len(provided.Messages) > 0 {
		stampedProvidedMessages = make([]*message.Message, 0, len(provided.Messages))
		for _, msg := range provided.Messages {
			stampedProvidedMessages = append(stampedProvidedMessages, p.withAgentRequestMessageSource(msg))
		}
	}

	return Context{
		Messages: stampedProvidedMessages,
		Tools:    provided.Tools,
	}, nil
}

// AfterRun persists context-related state after an agent invocation finishes.
func (p *ContextProvider) AfterRun(ctx AfterRunContext) error {
	if p.SourceID == "" {
		panic("SourceID is required")
	}
	if p.Store != nil {
		requestFilter := p.StoreRequestFilter
		if requestFilter == nil {
			requestFilter = messagefilter.NotSources(p.SourceID)
		}
		filteredRequestMessages, err := requestFilter(ctx.Context, ctx.RequestMessages)
		if err != nil {
			return err
		}

		responseFilter := p.StoreResponseFilter
		if responseFilter == nil {
			responseFilter = messagefilter.PassThrough
		}
		filteredResponseMessages, err := responseFilter(ctx.Context, ctx.ResponseMessages)
		if err != nil {
			return err
		}

		return p.Store(AfterRunContext{
			Context:          ctx.Context,
			Session:          ctx.Session,
			RequestMessages:  filteredRequestMessages,
			ResponseMessages: filteredResponseMessages,
			Tools:            ctx.Tools,
			InvokeError:      ctx.InvokeError,
		})
	}

	return nil
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
