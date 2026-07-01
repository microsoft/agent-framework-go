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

type contextProvider struct {
	strategy     Strategy
	sourceID     string
	stateKey     string
	tokenCounter TokenCounter
	logger       *slog.Logger
}

// NewContextProvider creates a context provider that applies compaction before each agent run.
//
// When a local session is available, the provider stores message-group state so subsequent runs can
// incrementally update the index. Without a session, it still performs stateless compaction over the
// current message list. Service-managed sessions are skipped because the service owns history.
func NewContextProvider(cfg ContextProviderConfig) agent.ContextProvider {
	if cfg.Strategy == nil {
		panic("Strategy is required")
	}
	cfg.SourceID = cmp.Or(cfg.SourceID, defaultProviderSourceID)
	cfg.StateKey = cmp.Or(cfg.StateKey, cfg.SourceID)
	return &contextProvider{
		strategy:     cfg.Strategy,
		sourceID:     cfg.SourceID,
		stateKey:     cfg.StateKey,
		tokenCounter: cfg.TokenCounter,
		logger:       cfg.Logger,
	}
}

func (p *contextProvider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	messages := invoking.Messages
	options := invoking.Options
	session, _ := agent.GetOption(options, agent.WithSession)
	if len(messages) == 0 {
		if p.logger != nil {
			p.logger.DebugContext(ctx, "compaction provider skipped", slog.String("reason", "no messages"))
		}
		return messages, options, nil
	}
	inputMessages := append([]*message.Message(nil), messages...)
	if session == nil {
		index := CreateMessageIndex(messages, p.tokenCounter)
		if _, err := p.strategy.Compact(ctx, index); err != nil {
			return nil, nil, err
		}
		return p.markGeneratedMessages(index.IncludedMessages(), inputMessages), options, nil
	}
	if session.ServiceID() != "" {
		if p.logger != nil {
			p.logger.DebugContext(ctx, "compaction provider skipped", slog.String("reason", "session managed by remote service"))
		}
		return messages, options, nil
	}

	var state providerState
	if _, err := session.Get(p.stateKey, &state); err != nil {
		return nil, nil, err
	}

	var index *MessageIndex
	if len(state.MessageGroups) > 0 {
		index = NewMessageIndex(state.MessageGroups, p.tokenCounter)
		index.Update(messages)
	} else {
		index = CreateMessageIndex(messages, p.tokenCounter)
	}

	beforeMessages := index.IncludedMessageCount()
	if p.logger != nil {
		p.logger.DebugContext(ctx, "applying compaction", slog.Int("messages", beforeMessages))
	}
	if _, err := p.strategy.Compact(ctx, index); err != nil {
		return nil, nil, err
	}
	afterMessages := index.IncludedMessageCount()
	if p.logger != nil && afterMessages < beforeMessages {
		p.logger.DebugContext(ctx, "compaction applied", slog.Int("before_messages", beforeMessages), slog.Int("after_messages", afterMessages))
	}

	state.MessageGroups = index.Groups
	session.Set(p.stateKey, state)
	return p.markGeneratedMessages(index.IncludedMessages(), inputMessages), options, nil
}

func (p *contextProvider) Invoked(context.Context, agent.InvokedContext) error {
	return nil
}

func (p *contextProvider) markGeneratedMessages(messages, inputMessages []*message.Message) []*message.Message {
	if len(messages) == 0 {
		return messages
	}
	originals := make(map[*message.Message]struct{}, len(inputMessages))
	for _, msg := range inputMessages {
		originals[msg] = struct{}{}
	}
	source := message.Source{Type: agent.SourceTypeContextProvider, ID: p.sourceID}
	for i, msg := range messages {
		if _, ok := originals[msg]; ok {
			continue
		}
		if msg == nil || msg.Source == source {
			continue
		}
		marked := msg.Clone()
		marked.Source = source
		messages[i] = marked
	}
	return messages
}
