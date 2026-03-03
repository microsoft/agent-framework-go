// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"cmp"
	"context"

	"github.com/microsoft/agent-framework-go/message"
)

// HistoryProvider provides an extensible base implementation for message history retrieval and persistence.
//
// It follows a two-phase lifecycle:
//   - Invoking: provide additional messages for the request.
//   - Invoked: persist newly processed messages after a successful invocation.
type HistoryProvider struct {
	// Optional key used by implementations to store data in Session state.
	// Defaults to "HistoryProvider" when empty.
	SourceID string

	// Optional filter applied to messages returned by Invoking.
	ProvideFilter func(ctx context.Context, messages []*message.Message) ([]*message.Message, error)

	// Optional filter applied to request messages before StoreHistory.
	// Defaults to excluding messages sourced from message history to prevent loops.
	StoreFilter func(ctx context.Context, messages []*message.Message) ([]*message.Message, error)

	// Optional retrieval hook. Defaults to returning no history.
	Provide func(ctx context.Context, session *Session, requestMessages []*message.Message) ([]*message.Message, error)

	// Optional storage hook. Defaults to no-op.
	Store func(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message) error

	// If true, history will still be stored even if the agent invocation returns an error. Defaults to false.
	StoreOnError bool
}

// Invoking returns the messages to use for the run.
func (p *HistoryProvider) Invoking(ctx context.Context, session *Session, requestMessages []*message.Message) ([]*message.Message, error) {
	var history []*message.Message
	var err error
	if p.Provide != nil {
		history, err = p.Provide(ctx, session, requestMessages)
		if err != nil {
			return nil, err
		}
	}
	if p.ProvideFilter != nil {
		history, err = p.ProvideFilter(ctx, history)
		if err != nil {
			return nil, err
		}
	}

	out := make([]*message.Message, 0, len(history)+len(requestMessages))
	for _, msg := range history {
		out = append(out, p.withAgentRequestMessageSource(msg))
	}
	out = append(out, requestMessages...)
	return out, nil
}

// Invoked persists new messages after an agent invocation finishes.
func (p *HistoryProvider) Invoked(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message, invokeError error) error {
	if invokeError != nil && !p.StoreOnError {
		return nil
	}
	filter := p.StoreFilter
	if filter == nil {
		filter = defaultExcludeHistoryFilter
	}
	filtered, err := filter(ctx, requestMessages)
	if err != nil {
		return err
	}
	if p.Store != nil {
		return p.Store(ctx, session, filtered, responseMessages)
	}
	return nil
}

func (p *HistoryProvider) withAgentRequestMessageSource(msg *message.Message) *message.Message {
	if msg == nil {
		return nil
	}
	out := msg.Clone()
	out.SourceID = cmp.Or(p.SourceID, "HistoryProvider")
	out.SourceType = "message_history"
	return out
}

// defaultExcludeHistoryFilter excludes messages previously sourced from message history.
func defaultExcludeHistoryFilter(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	out := make([]*message.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.SourceType != "message_history" {
			out = append(out, msg)
		}
	}
	return out, nil
}

type InMemoryHistoryProviderConfig struct {
	// Optional key used to store history in Session state. Defaults to "InMemoryHistoryProvider" when empty.
	StateKey string

	// Optional filter applied to messages returned by HistoryProvider.Invoking.
	ProvideFilter func(ctx context.Context, messages []*message.Message) ([]*message.Message, error)

	// Optional filter applied to request messages before HistoryProvider.Invoked.
	// Defaults to excluding messages sourced from message history to prevent loops.
	StoreFilter func(ctx context.Context, messages []*message.Message) ([]*message.Message, error)
}

func NewInMemoryHistoryProvider(config InMemoryHistoryProviderConfig) *HistoryProvider {
	stateKey := cmp.Or(config.StateKey, "InMemoryHistoryProvider")
	return &HistoryProvider{
		SourceID:      "InMemoryHistoryProvider",
		ProvideFilter: config.ProvideFilter,
		StoreFilter:   config.StoreFilter,
		Provide: func(ctx context.Context, session *Session, requestMessages []*message.Message) ([]*message.Message, error) {
			var state inmemoryState
			if _, err := session.Get(stateKey, &state); err != nil {
				return nil, err
			}
			return state.Messages, nil
		},
		Store: func(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message) error {
			var state inmemoryState
			if _, err := session.Get(stateKey, &state); err != nil {
				return err
			}
			state.Messages = append(state.Messages, requestMessages...)
			state.Messages = append(state.Messages, responseMessages...)
			session.Set(stateKey, state)
			return nil
		},
	}
}

type inmemoryState struct {
	Messages []*message.Message `json:"messages,omitempty"`
}
