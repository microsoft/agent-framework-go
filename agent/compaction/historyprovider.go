// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"cmp"
	"context"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"weak"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

const defaultHistoryProviderSourceID = "CompactionHistoryProvider"

// HistoryProviderConfig configures the provider created by [NewHistoryProvider].
type HistoryProviderConfig struct {
	// Strategy is the compaction strategy applied to persisted history.
	Strategy Strategy

	// SourceID identifies messages loaded from this provider.
	// When empty, a default compaction history provider source ID is used.
	SourceID string

	// StateKey identifies where provider state is stored in the session.
	// When empty, SourceID is used.
	StateKey string

	// StateInitializer returns initial messages on first use.
	// When nil, no initial messages are used.
	StateInitializer func(*agent.Session) []*message.Message

	// Optional filter applied to messages loaded from storage before they are included.
	// Defaults to passing all loaded messages through.
	ProvideOutputMessageFilter messagefilter.Filter

	// Optional filter applied to request messages before storing them.
	// Defaults to messages that did not come from a history provider.
	StoreInputRequestMessageFilter messagefilter.Filter

	// Optional filter applied to response messages before storing them.
	// Defaults to passing all response messages through.
	StoreInputResponseMessageFilter messagefilter.Filter

	// TokenCounter computes token counts for message groups.
	// When nil, token counts are estimated from UTF-8 byte counts.
	TokenCounter TokenCounter

	// Logger emits provider diagnostics when set.
	Logger *slog.Logger
}

type historyProviderState struct {
	Messages []*message.Message `json:"messages,omitempty"`
}

type historyProviderSessionLocks struct {
	locks           sync.Map // map[weak.Pointer[agent.Session]]*sync.Mutex
	nullSessionLock sync.Mutex
}

type historyProvider struct {
	config HistoryProviderConfig
	locks  *historyProviderSessionLocks
}

func (l *historyProviderSessionLocks) forOptions(options []agent.Option) *sync.Mutex {
	session, _ := agent.GetOption(options, agent.WithSession)
	if session == nil {
		return &l.nullSessionLock
	}
	key := weak.Make(session)
	if existing, ok := l.locks.Load(key); ok {
		return existing.(*sync.Mutex)
	}
	actual, loaded := l.locks.LoadOrStore(key, &sync.Mutex{})
	if !loaded {
		runtime.AddCleanup(session, func(k weak.Pointer[agent.Session]) {
			l.locks.Delete(k)
		}, key)
	}
	return actual.(*sync.Mutex)
}

// NewHistoryProvider creates a session-backed history provider that compacts stored history.
//
// The provider stores conversation history in the session like [agent.NewInMemoryHistoryProvider],
// but it automatically applies Strategy whenever history is loaded or updated. This gives history
// providers first-class reducer-trigger behavior without requiring a separate context provider.
func NewHistoryProvider(cfg HistoryProviderConfig) agent.HistoryProvider {
	if cfg.Strategy == nil {
		panic("Strategy is required")
	}
	cfg.SourceID = cmp.Or(cfg.SourceID, defaultHistoryProviderSourceID)
	cfg.StateKey = cmp.Or(cfg.StateKey, cfg.SourceID)
	return &historyProvider{config: cfg, locks: new(historyProviderSessionLocks)}
}

func (p *historyProvider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, error) {
	mu := p.locks.forOptions(invoking.Options)
	mu.Lock()
	defer mu.Unlock()

	session, _ := agent.GetOption(invoking.Options, agent.WithSession)
	if session == nil {
		return invoking.Messages, nil
	}
	state, err := getHistoryProviderState(session, p.config.StateKey, p.config.StateInitializer)
	if err != nil {
		return nil, err
	}

	history := slices.Clone(state.Messages)
	if p.config.ProvideOutputMessageFilter != nil {
		history, err = p.config.ProvideOutputMessageFilter(ctx, history)
		if err != nil {
			return nil, err
		}
	}

	source := message.Source{Type: agent.SourceTypeHistoryProvider, ID: p.config.SourceID}
	messages := make([]*message.Message, 0, len(history)+len(invoking.Messages))
	for _, msg := range history {
		if msg == nil {
			messages = append(messages, nil)
		} else {
			messages = append(messages, msg.WithSource(source))
		}
	}
	messages = append(messages, invoking.Messages...)

	compacted, err := compactHistory(ctx, p.config.Strategy, messages, p.config.TokenCounter, p.config.Logger)
	if err != nil {
		return nil, err
	}
	inputMessages := make(map[*message.Message]struct{}, len(invoking.Messages))
	for _, msg := range invoking.Messages {
		inputMessages[msg] = struct{}{}
	}
	for i, msg := range compacted {
		if msg == nil || msg.Source == source {
			continue
		}
		if _, ok := inputMessages[msg]; !ok {
			compacted[i] = msg.WithSource(source)
		}
	}
	return compacted, nil
}

func (p *historyProvider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	if invoked.Err != nil {
		return nil
	}

	requestFilter := p.config.StoreInputRequestMessageFilter
	if requestFilter == nil {
		requestFilter = messagefilter.NotSourceTypes(agent.SourceTypeHistoryProvider)
	}
	filteredRequest, err := requestFilter(ctx, slices.Clone(invoked.RequestMessages))
	if err != nil {
		return err
	}
	filteredResponse := invoked.ResponseMessages
	if p.config.StoreInputResponseMessageFilter != nil {
		filteredResponse, err = p.config.StoreInputResponseMessageFilter(ctx, slices.Clone(invoked.ResponseMessages))
		if err != nil {
			return err
		}
	}

	mu := p.locks.forOptions(invoked.Options)
	mu.Lock()
	defer mu.Unlock()

	session, _ := agent.GetOption(invoked.Options, agent.WithSession)
	if session == nil {
		return nil
	}
	state, err := getHistoryProviderState(session, p.config.StateKey, p.config.StateInitializer)
	if err != nil {
		return err
	}

	messages := slices.Clone(state.Messages)
	if p.config.ProvideOutputMessageFilter != nil {
		messages, err = p.config.ProvideOutputMessageFilter(ctx, messages)
		if err != nil {
			return err
		}
	}
	messages = append(messages, filteredRequest...)
	messages = append(messages, filteredResponse...)

	compacted, err := compactHistory(ctx, p.config.Strategy, messages, p.config.TokenCounter, p.config.Logger)
	if err != nil {
		return err
	}
	state.Messages = slices.Clone(compacted)
	session.Set(p.config.StateKey, state)
	return nil
}

func getHistoryProviderState(session *agent.Session, stateKey string, initializer func(*agent.Session) []*message.Message) (historyProviderState, error) {
	var state historyProviderState
	if ok, err := session.Get(stateKey, &state); err != nil {
		return state, err
	} else if ok {
		return state, nil
	}
	if initializer != nil {
		state.Messages = slices.Clone(initializer(session))
	}
	session.Set(stateKey, state)
	return state, nil
}

func compactHistory(ctx context.Context, strategy Strategy, messages []*message.Message, tokenCounter TokenCounter, logger *slog.Logger) ([]*message.Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	index := CreateMessageIndex(messages, tokenCounter)
	beforeMessages := index.IncludedMessageCount()
	if logger != nil {
		logger.DebugContext(ctx, "applying history compaction", slog.Int("messages", beforeMessages))
	}
	if _, err := strategy.Compact(ctx, index); err != nil {
		return nil, err
	}
	afterMessages := index.IncludedMessageCount()
	if logger != nil && afterMessages < beforeMessages {
		logger.DebugContext(ctx, "history compaction applied", slog.Int("before_messages", beforeMessages), slog.Int("after_messages", afterMessages))
	}
	return index.IncludedMessages(), nil
}
