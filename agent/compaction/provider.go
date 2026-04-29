// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"cmp"
	"context"
	"log/slog"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const defaultProviderSourceID = "CompactionProvider"

// ContextProviderConfig configures a compaction context provider.
type ContextProviderConfig struct {
	// Strategy is the compaction strategy to apply before each agent run.
	Strategy Strategy

	// SourceID identifies this provider in the agent context pipeline.
	// When empty, a default compaction provider source ID is used.
	SourceID string

	// StateKey identifies where provider state is stored in the session.
	// When empty, SourceID is used.
	StateKey string

	// TokenCounter computes token counts for message groups.
	// When nil, token counts are estimated from UTF-8 byte counts.
	TokenCounter TokenCounter

	// Logger emits provider diagnostics when set.
	Logger *slog.Logger
}

type providerState struct {
	MessageGroups []*MessageGroup `json:"messagegroups,omitempty"`
}

// NewContextProvider creates a context provider that applies compaction before each agent run.
//
// When a local session is available, the provider stores message-group state so subsequent runs can
// incrementally update the index. Without a session, it still performs stateless compaction over the
// current message list. Service-managed sessions are skipped because the service owns history.
func NewContextProvider(cfg ContextProviderConfig) *agent.ContextProvider {
	if cfg.Strategy == nil {
		panic("Strategy is required")
	}
	cfg.SourceID = cmp.Or(cfg.SourceID, defaultProviderSourceID)
	cfg.StateKey = cmp.Or(cfg.StateKey, cfg.SourceID)
	return &agent.ContextProvider{
		SourceID: cfg.SourceID,
		Provide: func(ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
			session, _ := agent.GetOption(options, agent.WithSession)
			if len(messages) == 0 {
				if cfg.Logger != nil {
					cfg.Logger.DebugContext(ctx, "compaction provider skipped", slog.String("reason", "no messages"))
				}
				return messages, options, nil
			}
			if session == nil {
				index := CreateMessageIndex(messages, cfg.TokenCounter)
				if _, err := cfg.Strategy.Compact(ctx, index); err != nil {
					return nil, nil, err
				}
				return index.IncludedMessages(), options, nil
			}
			if session.ServiceID() != "" {
				if cfg.Logger != nil {
					cfg.Logger.DebugContext(ctx, "compaction provider skipped", slog.String("reason", "session managed by remote service"))
				}
				return messages, options, nil
			}

			var state providerState
			if _, err := session.Get(cfg.StateKey, &state); err != nil {
				return nil, nil, err
			}

			var index *MessageIndex
			if len(state.MessageGroups) > 0 {
				index = NewMessageIndex(state.MessageGroups, cfg.TokenCounter)
				index.Update(messages)
			} else {
				index = CreateMessageIndex(messages, cfg.TokenCounter)
			}

			beforeMessages := index.IncludedMessageCount()
			if cfg.Logger != nil {
				cfg.Logger.DebugContext(ctx, "applying compaction", slog.Int("messages", beforeMessages))
			}
			if _, err := cfg.Strategy.Compact(ctx, index); err != nil {
				return nil, nil, err
			}
			afterMessages := index.IncludedMessageCount()
			if cfg.Logger != nil && afterMessages < beforeMessages {
				cfg.Logger.DebugContext(ctx, "compaction applied", slog.Int("before_messages", beforeMessages), slog.Int("after_messages", afterMessages))
			}

			state.MessageGroups = index.Groups
			session.Set(cfg.StateKey, state)
			return index.IncludedMessages(), options, nil
		},
	}
}
