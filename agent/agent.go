// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

// RunFunc is the provider function that executes an agent invocation.
// Implementations must treat the message and option slices, and existing
// messages, as read-only. Clone them before making changes.
type RunFunc = func(ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error]

// ProviderConfig configures the provider-specific implementation behind an Agent.
type ProviderConfig struct {
	// ProviderName identifies the underlying provider implementation.
	ProviderName string

	// Run executes a request and streams response updates.
	Run RunFunc

	// Middlewares wrap Run after agent history and context providers.
	Middlewares []Middleware

	// Format creates a provider response format for a structured output value.
	Format func(v any) (ResponseFormat, error)

	// Unmarshal decodes provider structured output into v using format.
	Unmarshal func(format ResponseFormat, data []byte, v any) error

	// CreateSession configures a provider-specific session. Implementations must
	// treat options as read-only and clone the slice before making changes.
	CreateSession func(ctx context.Context, session *Session, options ...Option) error

	// ServiceDoesNotManageHistory indicates that this provider never manages
	// conversation history server-side, even when a ServiceID is set on the
	// session. When true, the agent's HistoryProvider is always preserved
	// regardless of session service ID. Use this for providers like AGUI that
	// require the caller to supply the full conversation history on every turn
	// even after the service assigns a session or thread identifier.
	ServiceDoesNotManageHistory bool
}

// Config configures an Agent instance.
type Config struct {
	// ID uniquely identifies the agent. A random UUID is assigned when empty.
	ID string
	// Name is the display name used for agent-authored messages.
	Name string
	// Description describes the agent's purpose.
	Description string

	// HistoryProvider injects and persists conversation history around each agent run.
	// When nil, New uses a default in-memory history provider for local sessions.
	HistoryProvider HistoryProvider

	// ThrowOnHistoryProviderConflict controls whether a configured
	// HistoryProvider conflicting with service-managed history returns an error.
	// The default is true.
	ThrowOnHistoryProviderConflict *bool

	// WarnOnHistoryProviderConflict controls whether a warning is logged when a
	// configured HistoryProvider conflicts with service-managed history. The
	// default is true.
	WarnOnHistoryProviderConflict *bool

	// ClearOnHistoryProviderConflict controls whether the configured
	// HistoryProvider is cleared when it conflicts with service-managed history.
	// Returning an error takes precedence. The default is true.
	ClearOnHistoryProviderConflict *bool

	// ContextProviders inject and persist context around each agent run.
	ContextProviders []ContextProvider

	// Logger receives run, middleware, and provider diagnostics.
	Logger *slog.Logger

	// LogSensitiveData enables logging of sensitive request and response payloads.
	LogSensitiveData bool

	// DisableRunLogs disables automatic run logging when Logger is set.
	DisableRunLogs bool

	// Middlewares wrap the agent lifecycle before history and context providers.
	Middlewares []Middleware

	// MessageInjector configures mid-run message injection. Call its
	// EnqueueMessages method to queue messages. Nil disables message injection.
	MessageInjector *MessageInjector

	// Tools are added to every run.
	Tools []tool.Tool

	// RunOptions are prepended to the options for every run.
	RunOptions []Option
}

// New creates an Agent from provider and runtime configuration.
func New(prov ProviderConfig, cfg Config) *Agent {
	if prov.Run == nil {
		panic("Run function is required")
	}

	if cfg.ID == "" {
		cfg.ID = uuid.NewString()
	}

	cfg.RunOptions = slices.Clone(cfg.RunOptions)
	for _, tool := range cfg.Tools {
		if tool != nil {
			cfg.RunOptions = append(cfg.RunOptions, WithTool(tool))
		}
	}
	agentMiddlewares := cfg.Middlewares
	if cfg.Logger != nil && !cfg.DisableRunLogs {
		agentMiddlewares = append([]Middleware{newRunLoggerMiddleware(cfg.Logger, cfg.LogSensitiveData)}, agentMiddlewares...)
	}
	providerMiddlewares := make([]Middleware, 0, len(prov.Middlewares)+2)
	providerMiddlewares = append(providerMiddlewares, prov.Middlewares...)
	if cfg.MessageInjector != nil {
		providerMiddlewares = append(providerMiddlewares, MiddlewareFunc(cfg.MessageInjector.run))
	}
	if prov.Format != nil || prov.Unmarshal != nil {
		providerMiddlewares = append(providerMiddlewares, &structuredOutputMiddleware{
			format:    prov.Format,
			unmarshal: prov.Unmarshal,
		})
	}
	contextProviders := make([]ContextProvider, 0, len(cfg.ContextProviders))
	for _, provider := range cfg.ContextProviders {
		if provider != nil {
			contextProviders = append(contextProviders, provider)
		}
	}
	historyProvider := cfg.HistoryProvider
	var hasDefaultHistoryProvider bool
	if historyProvider == nil {
		historyProvider = NewInMemoryHistoryProvider(InMemoryHistoryProviderConfig{})
		hasDefaultHistoryProvider = true
	}
	a := &Agent{
		id:                           cfg.ID,
		name:                         cfg.Name,
		description:                  cfg.Description,
		provider:                     prov,
		runOptions:                   cfg.RunOptions,
		logger:                       cfg.Logger,
		historyProvider:              historyProvider,
		hasConfiguredHistory:         cfg.HistoryProvider != nil,
		hasDefaultHistoryProvider:    hasDefaultHistoryProvider,
		throwOnHistoryConflict:       cfg.ThrowOnHistoryProviderConflict == nil || *cfg.ThrowOnHistoryProviderConflict,
		warnOnHistoryConflict:        cfg.WarnOnHistoryProviderConflict == nil || *cfg.WarnOnHistoryProviderConflict,
		clearOnHistoryConflict:       cfg.ClearOnHistoryProviderConflict == nil || *cfg.ClearOnHistoryProviderConflict,
		providerDoesNotManageHistory: prov.ServiceDoesNotManageHistory,
		contextProviders:             contextProviders,
	}
	if len(providerMiddlewares) == 0 {
		a.providerPipeline = prov.Run
	} else {
		a.providerPipeline = compileRunChain(prov.Run, providerMiddlewares)
	}
	a.runPipeline = compileRunChain(a.invoke, agentMiddlewares)
	return a
}

// Agent coordinates message preparation, middleware, sessions, and provider execution.
type Agent struct {
	id               string
	name             string
	description      string
	provider         ProviderConfig
	providerPipeline RunFunc
	runPipeline      RunFunc

	runOptions []Option
	logger     *slog.Logger

	historyProvider HistoryProvider
	// historyCleared records that a run promoted its session to service-managed
	// history and cleared the configured provider globally (matching the .NET
	// clear-on-conflict semantics). It is set instead of mutating historyProvider
	// so a shared *Agent can be run concurrently without a data race.
	historyCleared       atomic.Bool
	hasConfiguredHistory bool
	// hasDefaultHistoryProvider is true when New synthesized the in-memory
	// history provider because Config.HistoryProvider was nil. The synthesized
	// provider is a local-session convenience and backs off for implicit per-run
	// sessions and service-managed sessions.
	hasDefaultHistoryProvider    bool
	throwOnHistoryConflict       bool
	warnOnHistoryConflict        bool
	clearOnHistoryConflict       bool
	providerDoesNotManageHistory bool
	contextProviders             []ContextProvider
}

// ID returns the agent's unique identifier.
func (a *Agent) ID() string {
	if a == nil {
		return ""
	}
	return a.id
}

// Name returns the agent's display name.
func (a *Agent) Name() string {
	if a == nil {
		return ""
	}
	return a.name
}

// Description returns the agent's description.
func (a *Agent) Description() string {
	if a == nil {
		return ""
	}
	return a.description
}

// ProviderName returns the name of the provider backing the agent.
func (a *Agent) ProviderName() string {
	if a == nil {
		return ""
	}
	return a.provider.ProviderName
}

// CreateSession creates a session for this agent.
func (a *Agent) CreateSession(ctx context.Context, options ...Option) (*Session, error) {
	session := &Session{}
	serviceID, _ := GetOption(options, WithServiceID)
	session.SetServiceID(serviceID)
	if a.provider.CreateSession != nil {
		if err := a.provider.CreateSession(ctx, session, options...); err != nil {
			return nil, err
		}
	}
	return session, nil
}

// RunText runs the agent with a single user text message.
func (a *Agent) RunText(ctx context.Context, msg string, options ...Option) ResponseStream {
	if strings.TrimSpace(msg) == "" {
		return errorResponseStream(errors.New("message cannot be blank"))
	}
	return a.Run(ctx, []*message.Message{message.NewText(msg)}, options...)
}

// RunMessage runs the agent with a single message.
func (a *Agent) RunMessage(ctx context.Context, msg *message.Message, options ...Option) ResponseStream {
	if msg == nil {
		return errorResponseStream(errors.New("message cannot be nil"))
	}
	return a.Run(ctx, []*message.Message{msg}, options...)
}

// Run executes the agent with the supplied messages and options.
func (a *Agent) Run(ctx context.Context, messages []*message.Message, options ...Option) ResponseStream {
	ctx, preparedMessages, options, err := a.prepareRun(ctx, messages, options)
	if err != nil {
		return errorResponseStream(err)
	}
	return ResponseStream(a.runPipeline(ctx, preparedMessages, options...))
}

func errorResponseStream(err error) ResponseStream {
	return func(yield func(*ResponseUpdate, error) bool) {
		yield(nil, err)
	}
}

func (a *Agent) invoke(ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return func(yield func(*ResponseUpdate, error) bool) {
		session, _ := GetOption(options, WithSession)
		rawContinuationToken, _ := GetOption(options, WithContinuationToken)
		continuationState, err := parseContinuationToken(rawContinuationToken)
		if err != nil {
			yield(nil, err)
			return
		}
		continuationToken := continuationState.InnerToken
		if rawContinuationToken != "" && continuationToken != rawContinuationToken {
			options = append(slices.Clone(options), WithContinuationToken(continuationToken))
		}
		noSession, _ := GetOption(options, noSessionProvided)
		stream, _ := GetOption(options, Stream)
		inputMessages := messages
		lifecycleOptions := withoutContinuationToken(options)

		historyProvider := a.historyProviderForRun(session, continuationToken, noSession)
		runContextProviders := continuationToken == "" && len(a.contextProviders) > 0
		if historyProvider != nil {
			var err error
			messages, err = historyProvider.Invoking(ctx, InvokingContext{Messages: messages, Options: lifecycleOptions})
			if err != nil {
				yield(nil, err)
				return
			}
		}

		if runContextProviders {
			options = lifecycleOptions
			for _, provider := range a.contextProviders {
				var err error
				messages, options, err = provider.Invoking(ctx, InvokingContext{Messages: messages, Options: options})
				if err != nil {
					yield(nil, err)
					return
				}
			}
		}

		var requestMessages []*message.Message
		if historyProvider != nil || runContextProviders {
			requestMessages = messages
		}
		trackContinuationUpdates := stream || continuationToken != ""

		// invocationState groups values captured by the provider sequence's yield
		// callback so range-over-function lowering heap-boxes one object instead of
		// allocating each captured local separately.
		var state struct {
			contextResponse     *Response
			historyResponse     *Response
			continuationUpdates []*ResponseUpdate
			runErr              error
			stopped             bool
		}
		if historyProvider != nil {
			state.historyResponse = new(Response)
		}
		if runContextProviders {
			state.contextResponse = new(Response)
		}
		if trackContinuationUpdates {
			state.continuationUpdates = cloneResponseUpdates(continuationState.ResponseUpdates)
		}

		for update, err := range a.providerPipeline(ctx, messages, options...) {
			if update != nil {
				a.setAuthor(update)
				if trackContinuationUpdates {
					state.continuationUpdates = append(state.continuationUpdates, cloneResponseUpdate(update))
				}
				if historyProvider != nil {
					state.historyResponse.Update(update)
				}
				if runContextProviders {
					state.contextResponse.Update(update)
				}
				if update.ContinuationToken != "" {
					var tokenInputMessages []*message.Message
					var tokenResponseUpdates []*ResponseUpdate
					if stream {
						tokenInputMessages = inputMessagesForContinuation(inputMessages, continuationState)
						tokenResponseUpdates = state.continuationUpdates
					}
					wrappedToken, err := wrapContinuationToken(update.ContinuationToken, tokenInputMessages, tokenResponseUpdates)
					if err != nil {
						yield(nil, err)
						return
					}
					update.ContinuationToken = wrappedToken
				}
			}
			if err != nil {
				state.runErr = err
			}
			state.stopped = !yield(update, err)
			if state.stopped || err != nil {
				break
			}
		}
		if state.stopped && state.runErr == nil {
			return
		}

		historyStoreProvider := historyProvider
		var storeResponseMessages []*message.Message
		if state.historyResponse != nil {
			storeResponseMessages = state.historyResponse.Messages
		}
		if continuationToken != "" {
			historyStoreProvider = a.historyProviderForContinuationStore(session, noSession)
			requestMessages = inputMessagesForContinuation(nil, continuationState)
			continuationResponse := responseFromUpdates(state.continuationUpdates)
			storeResponseMessages = continuationResponse.Messages
		}

		if historyStoreProvider != nil {
			storeHistory := a.shouldStoreHistoryProvider(historyStoreProvider, session)
			if state.runErr == nil {
				var err error
				storeHistory, err = a.handleHistoryProviderConflict(ctx, historyStoreProvider, session)
				if err != nil {
					if !state.stopped {
						yield(nil, err)
					}
					return
				}
				storeHistory = storeHistory && a.shouldStoreHistoryProvider(historyStoreProvider, session)
			}
			if storeHistory {
				if continuationToken == "" {
					state.historyResponse.Coalesce()
					storeResponseMessages = state.historyResponse.Messages
				}
				if err := historyStoreProvider.Invoked(ctx, InvokedContext{RequestMessages: requestMessages, ResponseMessages: storeResponseMessages, Options: withoutContinuationToken(options), Err: state.runErr}); err != nil {
					if !state.stopped {
						yield(nil, err)
					}
					return
				}
			}
		}

		if runContextProviders || continuationToken != "" && len(a.contextProviders) > 0 {
			var contextStoreResponseMessages []*message.Message
			if continuationToken != "" {
				continuationResponse := responseFromUpdates(state.continuationUpdates)
				contextStoreResponseMessages = continuationResponse.Messages
			} else {
				state.contextResponse.Coalesce()
				contextStoreResponseMessages = state.contextResponse.Messages
			}
			for _, provider := range a.contextProviders {
				if err := provider.Invoked(ctx, InvokedContext{RequestMessages: requestMessages, ResponseMessages: contextStoreResponseMessages, Options: withoutContinuationToken(options), Err: state.runErr}); err != nil {
					if !state.stopped {
						yield(nil, err)
					}
					return
				}
			}
		}
	}
}

func (a *Agent) setAuthor(update *ResponseUpdate) {
	if update == nil {
		return
	}
	if update.AgentID == "" {
		update.AgentID = a.id
	}
	if update.AuthorName == "" {
		update.AuthorName = a.name
	}
}

func withoutContinuationToken(options []Option) []Option {
	if !slices.ContainsFunc(options, func(opt Option) bool {
		_, ok := opt.(continuationTokenOpt)
		return ok
	}) {
		return options
	}
	return slices.DeleteFunc(slices.Clone(options), func(opt Option) bool {
		_, ok := opt.(continuationTokenOpt)
		return ok
	})
}

func (a *Agent) historyProviderForRun(session *Session, continuationToken string, noSession bool) HistoryProvider {
	if continuationToken != "" {
		return nil
	}
	return a.historyProviderForSession(session, noSession)
}

func (a *Agent) historyProviderForContinuationStore(session *Session, noSession bool) HistoryProvider {
	return a.historyProviderForSession(session, noSession)
}

func (a *Agent) historyProviderForSession(session *Session, noSession bool) HistoryProvider {
	if a.historyProvider == nil || session == nil || a.historyCleared.Load() {
		return nil
	}
	if !a.hasDefaultHistoryProvider {
		if session.ServiceID() != "" && !a.providerDoesNotManageHistory {
			return nil
		}
		return a.historyProvider
	}

	// The default in-memory provider only owns caller-provided local sessions.
	// Auto-created sessions are per-run and cannot preserve history across calls;
	// service-managed sessions use the provider service as the source of history.
	// Providers that never manage history server-side (e.g. AGUI) set
	// providerDoesNotManageHistory so the in-memory provider is kept regardless.
	if noSession || (session.ServiceID() != "" && !a.providerDoesNotManageHistory) {
		return nil
	}
	return a.historyProvider
}

func (a *Agent) shouldStoreHistoryProvider(provider HistoryProvider, session *Session) bool {
	if provider == nil {
		return false
	}
	if a.providerDoesNotManageHistory {
		// Provider never uses server-side history; always persist locally.
		return true
	}
	if session != nil && session.ServiceID() != "" {
		// Once the provider service owns the conversation history, no history
		// provider should persist the run locally, even if a configured provider
		// remains attached for future local sessions.
		return false
	}
	if !a.hasDefaultHistoryProvider {
		return true
	}

	// A provider can promote a local session to a service-managed one during the
	// run. Once that happens, the default in-memory provider should stop storing.
	return session != nil
}

func (a *Agent) handleHistoryProviderConflict(ctx context.Context, provider HistoryProvider, session *Session) (bool, error) {
	if provider == nil || !a.hasConfiguredHistory || session == nil || session.ServiceID() == "" || a.providerDoesNotManageHistory {
		return true, nil
	}

	if a.warnOnHistoryConflict && a.logger != nil {
		a.logger.WarnContext(ctx, "history provider conflicts with service-managed history", slog.String("service_id", session.ServiceID()))
	}
	if a.throwOnHistoryConflict {
		return false, errors.New("only Session.ServiceID or HistoryProvider may be used, but not both; the service returned an ID indicating service-managed history while the agent has a HistoryProvider configured")
	}
	if a.clearOnHistoryConflict {
		a.historyCleared.Store(true)
		return false, nil
	}
	return true, nil
}

func (a *Agent) prepareRun(ctx context.Context, messages []*message.Message, options []Option) (context.Context, []*message.Message, []Option, error) {
	// Prepend options from agent configuration.
	var optionsOwned bool
	if len(a.runOptions) != 0 {
		combined := make([]Option, 0, len(a.runOptions)+len(options)+2)
		combined = append(combined, a.runOptions...)
		options = append(combined, options...)
		optionsOwned = true
	}

	if session, ok := GetOption(options, WithSession); !ok || session == nil {
		if allowBackgroundResponses, ok := GetOption(options, AllowBackgroundResponses); ok && allowBackgroundResponses {
			// Background responses require an explicit session to avoid inconsistent
			// caller experience between initial and follow-up runs.
			return nil, nil, nil, errors.New("a session must be provided when AllowBackgroundResponses is enabled")
		}
		// Ensure a session is provided in the options.
		session, err := a.CreateSession(ctx, options...)
		if err != nil {
			return nil, nil, nil, err
		}
		if !optionsOwned {
			cloned := make([]Option, len(options), len(options)+2)
			copy(cloned, options)
			options = cloned
		}
		options = append(options, WithSession(session), noSessionProvided(true))
	}

	continuationToken, _ := GetOption(options, WithContinuationToken)
	if continuationToken != "" {
		if _, err := parseContinuationToken(continuationToken); err != nil {
			return nil, nil, nil, err
		}
	}
	if continuationToken != "" && len(messages) > 0 {
		return nil, nil, nil, errors.New("messages are not allowed when continuing a background response using a continuation token")
	}
	// Add agent identity to context so that middlewares can log it.
	ctx = context.WithValue(ctx, agentKey{}, a)

	return ctx, messages, options, nil
}

type agentKey struct{}

type noSessionOpt bool

func (o noSessionOpt) MAFValue() any { return bool(o) }

func noSessionProvided(v bool) Option {
	return noSessionOpt(v)
}

// AgentFromContext retrieves the agent that initiated the run from the context.
// Returns the agent and true if found, or nil and false otherwise.
func AgentFromContext(ctx context.Context) (*Agent, bool) {
	if v := ctx.Value(agentKey{}); v != nil {
		return v.(*Agent), true
	}
	return nil, false
}
