// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"errors"
	"iter"
	"runtime"
	"slices"
	"sync"
	"weak"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
)

// SourceTypeHistoryProvider represents a message that originated from a history provider.
const SourceTypeHistoryProvider message.SourceType = "history-provider"

const defaultInMemoryHistorySourceID = "in-memory"

// HistoryProvider retrieves chat history before an agent invocation and stores
// newly produced messages after an invocation.
//
// A history provider is only relevant when the underlying AI service does not
// manage chat history itself. Implementations are responsible for preserving
// message order and metadata, returning messages in chronological order, and
// applying storage-management strategies such as truncation, summarization, or
// archival when history grows large. It cannot add run options, tools,
// instructions, or other non-message context. Use [ContextProvider] when an
// extension needs to supply context that is not only conversation history.
//
// # Security considerations
//
// Agent Framework does not validate or filter the messages returned by the
// provider during load — they are accepted as-is and treated identically to
// user-supplied messages. Implementers must ensure that only trusted data is
// returned. If the underlying storage is compromised, adversarial content could
// influence LLM behavior via indirect prompt injection, for example by altering
// conversation context or impersonating different roles. Messages stored in chat
// history may contain PII and sensitive conversation content; implementers should
// consider encryption at rest and appropriate access controls for the storage backend.
type HistoryProvider interface {
	// Invoking returns the input messages with this provider's history applied.
	Invoking(context.Context, InvokingContext) ([]*message.Message, error)

	// Invoked persists history after an agent invocation finishes.
	Invoked(context.Context, InvokedContext) error
}

// HistoryProviderConfig configures the provider created by [NewHistoryProvider].
type HistoryProviderConfig struct {
	// Unique identifier for this provider instance (required).
	SourceID string

	// Optional filter applied to messages added by Provide before they are included.
	// Defaults to passing all added messages through.
	ProvideOutputMessageFilter messagefilter.Filter

	// Optional filter applied to request messages before Store.
	// Defaults to messages that did not come from a history provider.
	StoreInputRequestMessageFilter messagefilter.Filter

	// Optional filter applied to response messages before Store.
	// Defaults to passing all response messages through.
	StoreInputResponseMessageFilter messagefilter.Filter

	// Optional retrieval hook that returns additional history messages in chronological order.
	Provide func(context.Context, InvokingContext) ([]*message.Message, error)

	// Optional storage hook. Defaults to no-op.
	Store func(context.Context, InvokedContext) error
}

type defaultHistoryProvider struct {
	config HistoryProviderConfig
}

// NewHistoryProvider creates the default additive history provider.
//
// The provider treats Provide results as additional history messages, filters
// those messages when ProvideOutputMessageFilter is set, source-stamps them,
// prepends them to caller-provided request messages, filters stored request and
// response messages, and skips Store when the run fails.
//
// It panics if SourceID is empty.
func NewHistoryProvider(config HistoryProviderConfig) HistoryProvider {
	if config.SourceID == "" {
		panic("SourceID is required")
	}
	return &defaultHistoryProvider{config: config}
}

// Invoking returns the input messages with this provider's history applied.
func (p *defaultHistoryProvider) Invoking(ctx context.Context, invoking InvokingContext) ([]*message.Message, error) {
	if p.config.SourceID == "" {
		panic("SourceID is required")
	}
	if p.config.Provide == nil {
		return invoking.Messages, nil
	}

	providedMessages, err := p.config.Provide(ctx, invoking)
	if err != nil {
		return nil, err
	}

	if p.config.ProvideOutputMessageFilter != nil {
		providedMessages, err = p.config.ProvideOutputMessageFilter(ctx, slices.Clone(providedMessages))
		if err != nil {
			return nil, err
		}
	}

	if len(providedMessages) == 0 {
		return invoking.Messages, nil
	}

	outMessages := make([]*message.Message, 0, len(providedMessages)+len(invoking.Messages))
	source := message.Source{Type: SourceTypeHistoryProvider, ID: p.config.SourceID}
	for _, msg := range providedMessages {
		outMessages = append(outMessages, msg.WithSource(source))
	}
	outMessages = append(outMessages, invoking.Messages...)

	return outMessages, nil
}

// Invoked persists history after an agent invocation finishes.
func (p *defaultHistoryProvider) Invoked(ctx context.Context, invoked InvokedContext) error {
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
		requestFilter = notSourceTypes(SourceTypeHistoryProvider)
	}
	filteredReq, err := requestFilter(ctx, slices.Clone(invoked.RequestMessages))
	if err != nil {
		return err
	}
	filteredResp := invoked.ResponseMessages
	if responseFilter := p.config.StoreInputResponseMessageFilter; responseFilter != nil {
		filteredResp, err = responseFilter(ctx, slices.Clone(invoked.ResponseMessages))
		if err != nil {
			return err
		}
	}
	return p.config.Store(ctx, InvokedContext{RequestMessages: filteredReq, ResponseMessages: filteredResp, Options: invoked.Options, Err: invoked.Err})
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

// InMemoryHistoryProviderConfig configures the provider created by [NewInMemoryHistoryProvider].
type InMemoryHistoryProviderConfig struct {
	// SourceID identifies messages loaded from this provider.
	// When empty, a default in-memory history provider source ID is used.
	SourceID string

	// StateKey identifies where provider state is stored in the session.
	// When empty, SourceID is used.
	StateKey string

	// StateInitializer returns initial messages on first use.
	// When nil, no initial messages are used.
	StateInitializer func(*Session) []*message.Message

	// Optional filter applied to messages loaded from storage before they are included.
	// Defaults to passing all loaded messages through.
	ProvideOutputMessageFilter messagefilter.Filter

	// Optional filter applied to request messages before storing them.
	// Defaults to messages that did not come from a history provider.
	StoreInputRequestMessageFilter messagefilter.Filter

	// Optional filter applied to response messages before storing them.
	// Defaults to passing all response messages through.
	StoreInputResponseMessageFilter messagefilter.Filter
}

type inMemoryHistoryProviderState struct {
	Messages []*message.Message `json:"messages,omitempty"`
}

type historySessionLocks struct {
	locks           sync.Map // map[weak.Pointer[Session]]*sync.Mutex
	nullSessionLock sync.Mutex
}

func (l *historySessionLocks) forOptions(options []Option) *sync.Mutex {
	session, ok := GetOption(options, WithSession)
	if !ok || session == nil {
		return &l.nullSessionLock
	}
	key := weak.Make(session)
	if existing, ok := l.locks.Load(key); ok {
		return existing.(*sync.Mutex)
	}
	actual, loaded := l.locks.LoadOrStore(key, &sync.Mutex{})
	if !loaded {
		runtime.AddCleanup(session, func(k weak.Pointer[Session]) {
			l.locks.Delete(k)
		}, key)
	}
	return actual.(*sync.Mutex)
}

// NewInMemoryHistoryProvider creates a history provider that stores conversation history in the session.
func NewInMemoryHistoryProvider(config InMemoryHistoryProviderConfig) HistoryProvider {
	locks := new(historySessionLocks)
	sourceID := config.SourceID
	if sourceID == "" {
		sourceID = defaultInMemoryHistorySourceID
	}
	stateKey := config.StateKey
	if stateKey == "" {
		stateKey = sourceID
	}
	historyConfig := HistoryProviderConfig{
		SourceID:                        sourceID,
		ProvideOutputMessageFilter:      config.ProvideOutputMessageFilter,
		StoreInputRequestMessageFilter:  config.StoreInputRequestMessageFilter,
		StoreInputResponseMessageFilter: config.StoreInputResponseMessageFilter,
		Provide: func(_ context.Context, invoking InvokingContext) ([]*message.Message, error) {
			mu := locks.forOptions(invoking.Options)
			mu.Lock()
			defer mu.Unlock()
			session, _ := GetOption(invoking.Options, WithSession)
			if session == nil {
				return nil, nil
			}
			state, err := getInMemoryHistoryProviderState(session, stateKey, config.StateInitializer)
			if err != nil {
				return nil, err
			}
			if len(state.Messages) == 0 {
				return nil, nil
			}
			return slices.Clone(state.Messages), nil
		},
		Store: func(_ context.Context, invoked InvokedContext) error {
			mu := locks.forOptions(invoked.Options)
			mu.Lock()
			defer mu.Unlock()
			session, _ := GetOption(invoked.Options, WithSession)
			if session == nil {
				return nil
			}
			state, err := getInMemoryHistoryProviderState(session, stateKey, config.StateInitializer)
			if err != nil {
				return err
			}
			state.Messages = append(state.Messages, invoked.RequestMessages...)
			state.Messages = append(state.Messages, invoked.ResponseMessages...)
			session.Set(stateKey, state)
			return nil
		},
	}
	return NewHistoryProvider(historyConfig)
}

func getInMemoryHistoryProviderState(session *Session, stateKey string, initializer func(*Session) []*message.Message) (inMemoryHistoryProviderState, error) {
	var state inMemoryHistoryProviderState
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

// This ID tells outer middleware that history is retained downstream. It never
// reaches the service, and is only used with per-service-call persistence.
const localHistoryConversationID = "_agent_local_chat_history"

func (a *Agent) persistServiceCallHistory(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return func(yield func(*ResponseUpdate, error) bool) {
		session, _ := GetOption(options, WithSession)
		serviceID, _ := GetOption(options, WithServiceID)
		if serviceID == localHistoryConversationID {
			serviceID = ""
			options = append(slices.Clone(options), WithServiceID(""))
		}
		continuationToken, _ := GetOption(options, WithContinuationToken)
		allowBackground, _ := GetOption(options, AllowBackgroundResponses)
		backgroundOrContinuation := continuationToken != "" || allowBackground
		serviceManaged := serviceID != ""
		// Simulation is selected by the request, independently of any real
		// conversation ID returned later in the stream.
		skipSimulation := serviceManaged || backgroundOrContinuation
		messagesForService := messages
		if !skipSimulation {
			if history := a.historyProviderForSession(session, options, false); history != nil {
				var err error
				messagesForService, err = history.Invoking(ctx, InvokingContext{Messages: messages, Options: options})
				if err != nil {
					yield(nil, err)
					return
				}
			}
		}

		stream, _ := GetOption(options, Stream)
		var response Response
		var runErr error
		var stopped bool
		for update, err := range next(ctx, messagesForService, options...) {
			if err != nil {
				runErr = err
				break
			}
			// Assemble the actual service response before outer middleware can
			// replace function calls with approval requests or stamp its own ID.
			response.Update(update)
			if stream {
				if update != nil {
					if update.ConversationID != nil && *update.ConversationID != "" {
						serviceManaged = true
					} else if !skipSimulation {
						out := *update
						out.ConversationID = new(localHistoryConversationID)
						update = &out
					}
				}
				if !yield(update, nil) {
					stopped = true
					break
				}
			}
		}
		if stopped {
			if len(messages) > 0 {
				// Preserve inputs on a cooperative pause, notably tool results
				// paired with calls already stored by an earlier service request.
				// Cleanup must not replace the caller's reason for stopping.
				persistCtx := ctx
				if ctx.Err() != nil {
					persistCtx = context.WithoutCancel(ctx)
				}
				_ = a.notifyServiceCallProviders(persistCtx, session, messages, nil, options, nil)
			}
			return
		}
		if runErr != nil {
			if err := a.notifyServiceCallProviders(ctx, session, messages, nil, options, runErr); err != nil {
				runErr = errors.Join(runErr, err)
			}
			yield(nil, runErr)
			return
		}
		response.Coalesce()
		if err := a.notifyServiceCallProviders(ctx, session, messages, response.Messages, options, nil); err != nil {
			yield(nil, err)
			return
		}
		// Background and continuation calls still notify providers above, but
		// leave session IDs to the outer, continuation-aware lifecycle.
		if !backgroundOrContinuation {
			if serviceManaged || response.ConversationID != nil && *response.ConversationID != "" {
				if err := a.updateSessionConversationID(ctx, session, response.ConversationID); err != nil {
					yield(nil, err)
					return
				}
			} else {
				response.ConversationID = new(localHistoryConversationID)
				session.SetServiceID(localHistoryConversationID)
			}
		}
		if !stream {
			for _, update := range response.ToUpdates() {
				if !yield(update, nil) {
					return
				}
			}
		}
	}
}

func (a *Agent) notifyServiceCallProviders(ctx context.Context, session *Session, requests, responses []*message.Message, options []Option, runErr error) error {
	invoked := InvokedContext{RequestMessages: requests, ResponseMessages: responses, Options: options, Err: runErr}
	if history := a.historyProviderForSession(session, options, false); history != nil {
		if err := history.Invoked(ctx, invoked); err != nil {
			return err
		}
	}
	for _, provider := range a.contextProviders {
		if err := provider.Invoked(ctx, invoked); err != nil {
			return err
		}
	}
	return nil
}
