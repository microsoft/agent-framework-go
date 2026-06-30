// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"slices"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

// SourceTypeHistoryProvider represents a message that originated from a history provider.
const SourceTypeHistoryProvider message.SourceType = "history-provider"

const defaultInMemoryHistorySourceID = "in-memory"

// HistoryProvider provides a conversation-history subset of [ContextProvider]
// behavior around an agent invocation.
//
// A history provider can retrieve prior conversation messages, combine and
// filter them with the current request messages, mark added messages with this
// provider's source, and persist filtered request and response messages after
// the run completes. It cannot add or replace run options, tools, instructions, or
// other non-message context. Use [ContextProvider] when an extension needs to
// supply options or context that is not only conversation history.
type HistoryProvider struct {
	// Unique identifier for this provider instance (required).
	SourceID string

	// Optional filter applied to messages added by Provide before they are included.
	// Defaults to passing all added messages through.
	ProvideFilter messagefilter.Filter

	// Optional filter applied to request messages before Store.
	// Defaults to messages that did not come from a history provider.
	StoreRequestFilter messagefilter.Filter

	// Optional filter applied to response messages before Store.
	// Defaults to passing all response messages through.
	StoreResponseFilter messagefilter.Filter

	// Optional retrieval hook that returns updated history and request messages.
	// Messages that are not pointer-identical to the original input messages are marked with this provider's source.
	// Defaults to returning the original messages unchanged.
	Provide func(context.Context, []*message.Message, ...Option) ([]*message.Message, error)

	// Optional storage hook. Defaults to no-op.
	Store func(context.Context, []*message.Message, []*message.Message, ...Option) error
}

// BeforeRun returns the input messages with this provider's history applied.
func (p *HistoryProvider) BeforeRun(ctx context.Context, messages []*message.Message, options ...Option) ([]*message.Message, error) {
	if p.SourceID == "" {
		panic("SourceID is required")
	}
	if p.Provide == nil {
		return messages, nil
	}

	outMessages, err := p.Provide(ctx, messages, options...)
	if err != nil {
		return nil, err
	}

	if outMessages == nil {
		outMessages = messages
	}

	if p.ProvideFilter != nil {
		outMessages, err = p.filterProvidedMessages(ctx, outMessages, messages)
		if err != nil {
			return nil, err
		}
	}

	markNewMessagesWithSource(outMessages, messages, message.Source{Type: SourceTypeHistoryProvider, ID: p.SourceID}, true)

	return outMessages, nil
}

// AfterRun persists history after an agent invocation finishes.
func (p *HistoryProvider) AfterRun(ctx context.Context, requestMessages, responseMessages []*message.Message, options ...Option) error {
	if p.SourceID == "" {
		panic("SourceID is required")
	}
	requestFilter := p.StoreRequestFilter
	if requestFilter == nil {
		requestFilter = notSourceTypes(SourceTypeHistoryProvider)
	}
	return runStoreHook(ctx, p.Store, requestFilter, p.StoreResponseFilter, requestMessages, responseMessages, options)
}

func notSourceTypes(sourceTypes ...message.SourceType) messagefilter.Filter {
	return func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
		return slices.DeleteFunc(messages, func(msg *message.Message) bool {
			if msg == nil {
				return false
			}
			return slices.Contains(sourceTypes, msg.Source.Type)
		}), nil
	}
}

func (p *HistoryProvider) filterProvidedMessages(ctx context.Context, outMessages, inMessages []*message.Message) ([]*message.Message, error) {
	originals := make(map[*message.Message]struct{}, len(inMessages))
	for _, msg := range inMessages {
		originals[msg] = struct{}{}
	}
	provided := make([]*message.Message, 0, len(outMessages))
	for _, msg := range outMessages {
		if _, ok := originals[msg]; ok {
			continue
		}
		provided = append(provided, msg)
	}
	filtered, err := p.ProvideFilter(ctx, provided)
	if err != nil {
		return nil, err
	}
	kept := make(map[*message.Message]struct{}, len(filtered))
	for _, msg := range filtered {
		kept[msg] = struct{}{}
	}
	return slices.DeleteFunc(outMessages, func(msg *message.Message) bool {
		if _, ok := originals[msg]; ok {
			return false
		}
		_, ok := kept[msg]
		return !ok
	}), nil
}

// NewInMemoryHistoryProvider creates a history provider that stores conversation history in the session.
// If sourceID is empty, it defaults to "in-memory".
func NewInMemoryHistoryProvider(sourceID string) *HistoryProvider {
	if sourceID == "" {
		sourceID = defaultInMemoryHistorySourceID
	}
	return &HistoryProvider{
		SourceID: sourceID,
		Provide: func(_ context.Context, msgs []*message.Message, options ...Option) ([]*message.Message, error) {
			session, _ := GetOption(options, WithSession)
			if session == nil {
				return msgs, nil
			}
			var state inmemoryState
			if _, err := session.Get(sourceID, &state); err != nil {
				return nil, err
			}
			if len(state.Messages) == 0 {
				return msgs, nil
			}
			messages := make([]*message.Message, 0, len(state.Messages)+len(msgs))
			messages = append(messages, state.Messages...)
			messages = append(messages, msgs...)
			return messages, nil
		},
		Store: func(_ context.Context, requestMessages, responseMessages []*message.Message, options ...Option) error {
			session, _ := GetOption(options, WithSession)
			if session == nil {
				return nil
			}
			var state inmemoryState
			if _, err := session.Get(sourceID, &state); err != nil {
				return err
			}
			state.Messages = append(state.Messages, requestMessages...)
			state.Messages = append(state.Messages, responseMessages...)
			session.Set(sourceID, state)
			return nil
		},
	}
}

type inmemoryState struct {
	Messages []*message.Message `json:"messages,omitempty"`
}
