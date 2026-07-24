// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"slices"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

// RunFunc is the provider function that executes an agent invocation.
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

	// CreateSession configures a provider-specific session.
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

	// AllowHistoryProviderConflict prevents returning an error when a configured
	// HistoryProvider conflicts with service-managed history.
	AllowHistoryProviderConflict bool

	// SuppressHistoryProviderConflictWarning prevents logging a warning when a
	// configured HistoryProvider conflicts with service-managed history.
	SuppressHistoryProviderConflictWarning bool

	// KeepHistoryProviderOnConflict prevents clearing the configured HistoryProvider
	// when it conflicts with service-managed history. Returning an error takes precedence.
	KeepHistoryProviderOnConflict bool

	// ContextProviders inject and persist context around each agent run.
	ContextProviders []ContextProvider

	// DisableFuncAutoCall tells provider constructors not to add automatic function-tool calling middleware.
	DisableFuncAutoCall bool

	// Logger receives run, middleware, and provider diagnostics.
	Logger *slog.Logger

	// LogSensitiveData enables logging of sensitive request and response payloads.
	LogSensitiveData bool

	// DisableRunLogs disables automatic run logging when Logger is set.
	DisableRunLogs bool

	// Middlewares wrap the agent lifecycle before history and context providers.
	Middlewares []Middleware

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
	cfg.Middlewares = slices.Clone(cfg.Middlewares)
	if cfg.Logger != nil && !cfg.DisableRunLogs {
		cfg.Middlewares = append([]Middleware{newRunLoggerMiddleware(cfg.Logger, cfg.LogSensitiveData)}, cfg.Middlewares...)
	}
	prov.Middlewares = slices.Clone(prov.Middlewares)
	if prov.Format != nil || prov.Unmarshal != nil {
		prov.Middlewares = append(prov.Middlewares, &structuredOutputMiddleware{
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
	prov.Middlewares = append(prov.Middlewares, authorMiddleware(cfg.ID, cfg.Name))
	return &Agent{
		id:                           cfg.ID,
		name:                         cfg.Name,
		description:                  cfg.Description,
		provider:                     prov,
		runOptions:                   cfg.RunOptions,
		middlewares:                  cfg.Middlewares,
		logger:                       cfg.Logger,
		historyProvider:              historyProvider,
		hasConfiguredHistory:         cfg.HistoryProvider != nil,
		hasDefaultHistoryProvider:    hasDefaultHistoryProvider,
		allowHistoryConflict:         cfg.AllowHistoryProviderConflict,
		suppressHistoryWarning:       cfg.SuppressHistoryProviderConflictWarning,
		keepHistoryOnConflict:        cfg.KeepHistoryProviderOnConflict,
		providerDoesNotManageHistory: prov.ServiceDoesNotManageHistory,
		contextProviders:             contextProviders,
	}
}

// Agent coordinates message preparation, middleware, sessions, and provider execution.
type Agent struct {
	id          string
	name        string
	description string
	provider    ProviderConfig

	middlewares []Middleware
	runOptions  []Option
	logger      *slog.Logger

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
	allowHistoryConflict         bool
	suppressHistoryWarning       bool
	keepHistoryOnConflict        bool
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
	return a.Run(ctx, []*message.Message{message.NewText(msg)}, options...)
}

// RunMessage runs the agent with a single message.
func (a *Agent) RunMessage(ctx context.Context, msg *message.Message, options ...Option) ResponseStream {
	return a.Run(ctx, []*message.Message{msg}, options...)
}

// Run executes the agent with the supplied messages and options.
func (a *Agent) Run(ctx context.Context, messages []*message.Message, options ...Option) ResponseStream {
	ctx, preparedMessages, options, err := a.prepareRun(ctx, messages, options)
	if err != nil {
		return func(yield func(*ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	return ResponseStream(a.run(ctx, preparedMessages, options...))
}

func (a *Agent) run(ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return runChain(ctx, a.invoke, a.middlewares, messages, options...)
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
		inputMessages := slices.Clone(messages)
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
			options = slices.Clone(lifecycleOptions)
			for _, provider := range a.contextProviders {
				var err error
				messages, options, err = provider.Invoking(ctx, InvokingContext{Messages: messages, Options: options})
				if err != nil {
					yield(nil, err)
					return
				}
			}
		}

		requestMessages := slices.Clone(messages)
		var contextResponse Response
		var historyResponse Response
		continuationUpdates := cloneResponseUpdates(continuationState.ResponseUpdates)
		var runErr error
		var stopped bool

		for update, err := range runChain(ctx, a.provider.Run, a.provider.Middlewares, messages, options...) {
			if update != nil {
				continuationUpdates = append(continuationUpdates, cloneResponseUpdate(update))
				historyResponse.Update(update)
				if runContextProviders {
					contextResponse.Update(update)
				}
				if update.ContinuationToken != "" {
					var tokenInputMessages []*message.Message
					var tokenResponseUpdates []*ResponseUpdate
					if stream {
						tokenInputMessages = inputMessagesForContinuation(inputMessages, continuationState)
						tokenResponseUpdates = continuationUpdates
					}
					wrappedToken, err := wrapContinuationToken(update.ContinuationToken, tokenInputMessages, tokenResponseUpdates)
					if err != nil {
						if !stopped {
							yield(nil, err)
						}
						return
					}
					update.ContinuationToken = wrappedToken
				}
			}
			if err != nil {
				runErr = err
				stopped = !yield(update, err)
				break
			}
			if !yield(update, nil) {
				stopped = true
				break
			}
		}

		historyStoreProvider := historyProvider
		storeRequestMessages := requestMessages
		storeResponseMessages := historyResponse.Messages
		if continuationToken != "" {
			historyStoreProvider = a.historyProviderForContinuationStore(session, noSession)
			storeRequestMessages = inputMessagesForContinuation(nil, continuationState)
			continuationResponse := responseFromUpdates(continuationUpdates)
			storeResponseMessages = continuationResponse.Messages
		}

		if historyStoreProvider != nil {
			storeHistory := a.shouldStoreHistoryProvider(historyStoreProvider, session)
			if runErr == nil {
				var err error
				storeHistory, err = a.handleHistoryProviderConflict(ctx, historyStoreProvider, session)
				if err != nil {
					if !stopped {
						yield(nil, err)
					}
					return
				}
				storeHistory = storeHistory && a.shouldStoreHistoryProvider(historyStoreProvider, session)
			}
			if storeHistory {
				if continuationToken == "" {
					historyResponse.Coalesce()
					storeResponseMessages = historyResponse.Messages
				}
				if err := historyStoreProvider.Invoked(ctx, InvokedContext{RequestMessages: slices.Clone(storeRequestMessages), ResponseMessages: slices.Clone(storeResponseMessages), Options: withoutContinuationToken(options), Err: runErr}); err != nil {
					if !stopped {
						yield(nil, err)
					}
					return
				}
			}
		}

		if runContextProviders || continuationToken != "" && len(a.contextProviders) > 0 {
			var contextStoreResponseMessages []*message.Message
			if continuationToken != "" {
				continuationResponse := responseFromUpdates(continuationUpdates)
				contextStoreResponseMessages = continuationResponse.Messages
			} else {
				contextResponse.Coalesce()
				contextStoreResponseMessages = contextResponse.Messages
			}
			for _, provider := range a.contextProviders {
				if err := provider.Invoked(ctx, InvokedContext{RequestMessages: slices.Clone(storeRequestMessages), ResponseMessages: slices.Clone(contextStoreResponseMessages), Options: withoutContinuationToken(options), Err: runErr}); err != nil {
					if !stopped {
						yield(nil, err)
					}
					return
				}
			}
		}
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
	if !a.hasDefaultHistoryProvider {
		return true
	}
	if a.providerDoesNotManageHistory {
		// Provider never uses server-side history; always persist locally.
		return true
	}

	// A provider can promote a local session to a service-managed one during the
	// run. Once that happens, the default in-memory provider should stop storing.
	return session != nil && session.ServiceID() == ""
}

func (a *Agent) handleHistoryProviderConflict(ctx context.Context, provider HistoryProvider, session *Session) (bool, error) {
	if provider == nil || !a.hasConfiguredHistory || session == nil || session.ServiceID() == "" || a.providerDoesNotManageHistory {
		return true, nil
	}

	if !a.suppressHistoryWarning && a.logger != nil {
		a.logger.WarnContext(ctx, "history provider conflicts with service-managed history", slog.String("service_id", session.ServiceID()))
	}
	if !a.allowHistoryConflict {
		return false, errors.New("only Session.ServiceID or HistoryProvider may be used, but not both; the service returned an ID indicating service-managed history while the agent has a HistoryProvider configured")
	}
	if !a.keepHistoryOnConflict {
		a.historyCleared.Store(true)
		return false, nil
	}
	return true, nil
}

func (a *Agent) prepareRun(ctx context.Context, messages []*message.Message, options []Option) (context.Context, []*message.Message, []Option, error) {
	// Prepend options from agent configuration.
	if len(a.runOptions) != 0 {
		options = append(a.runOptions, options...)
	}

	if _, ok := GetOption(options, WithSession); !ok {
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

func authorMiddleware(id, name string) Middleware {
	return MiddlewareFunc(func(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
		return func(yield func(*ResponseUpdate, error) bool) {
			for update, err := range next(ctx, messages, options...) {
				if update != nil {
					if update.AgentID == "" {
						update.AgentID = id
					}
					if update.AuthorName == "" {
						update.AuthorName = name
					}
				}
				if !yield(update, err) {
					return
				}
			}
		}
	})
}

type agentKey struct{}

type noSessionOpt bool

func (o noSessionOpt) Value() any { return bool(o) }

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
