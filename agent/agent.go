// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/microsoft/agent-framework-go/middleware/autocall"
	"github.com/microsoft/agent-framework-go/middleware/structuredoutput"
)

type ProviderConfig struct {
	ProviderName string

	// Required functions
	Run func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error]

	// Optional functions
	CreateSession    func(ctx context.Context, options ...agentopt.Option) (*memory.Session, error)
	MarshalSession   func(ctx context.Context, session *memory.Session) ([]byte, error)
	UnmarshalSession func(ctx context.Context, data []byte) (*memory.Session, error)
	FormatOfFn       func(v any) (format.Format, error)
	UnmarshalFn      func(format format.Format, data []byte, v any) error
}

type Config struct {
	ID          string
	Name        string
	Description string

	Instructions string

	ContextProviders []*memory.ContextProvider

	DisableFuncAutoCall bool

	Logger           *slog.Logger
	LogSensitiveData bool

	Middlewares []middleware.Middleware
	RunOptions  []agentopt.Option
}

func New(prov ProviderConfig, cfg Config) *Agent {
	if prov.Run == nil {
		panic("Run function is required")
	}

	if cfg.ID == "" {
		cfg.ID = uuid.NewString()
	}

	cfg.RunOptions = slices.Clone(cfg.RunOptions)
	cfg.Middlewares = slices.Clone(cfg.Middlewares)
	if !cfg.DisableFuncAutoCall {
		cfg.Middlewares = append(cfg.Middlewares,
			autocall.New(autocall.Config{
				Logger:           cfg.Logger,
				LogSensitiveData: cfg.LogSensitiveData,
			}),
		)
	}
	providers := make([]*memory.ContextProvider, 0, len(cfg.ContextProviders)+1)
	for _, provider := range cfg.ContextProviders {
		if provider != nil {
			providers = append(providers, provider)
		}
	}
	if prov.FormatOfFn != nil && prov.UnmarshalFn != nil {
		cfg.Middlewares = append(cfg.Middlewares,
			structuredoutput.New(structuredoutput.Config{
				Format:    prov.FormatOfFn,
				Unmarshal: prov.UnmarshalFn,
			}),
		)
	}
	cfg.Middlewares = append([]middleware.Middleware{providerMiddleware(providers)}, cfg.Middlewares...)
	cfg.Middlewares = append(cfg.Middlewares, authorMiddleware(cfg.ID, cfg.Name))
	return &Agent{
		id:               cfg.ID,
		name:             cfg.Name,
		description:      cfg.Description,
		providerName:     prov.ProviderName,
		instructions:     cfg.Instructions,
		createSession:    prov.CreateSession,
		marshalSession:   prov.MarshalSession,
		unmarshalSession: prov.UnmarshalSession,
		run:              prov.Run,
		runOptions:       cfg.RunOptions,
		middlewares:      cfg.Middlewares,
	}
}

// ResponseStream represents an execution of the agent.
type ResponseStream iter.Seq2[*message.ResponseUpdate, error]

// Collect gathers all response updates into a single Response object.
func (r ResponseStream) Collect() (*message.Response, error) {
	var resp message.Response
	for update, err := range r {
		if err != nil {
			return nil, err
		}
		resp.Update(update)
	}
	resp.Coalesce()
	return &resp, nil
}

type Agent struct {
	id           string
	name         string
	description  string
	providerName string

	instructions string

	middlewares []middleware.Middleware
	runOptions  []agentopt.Option

	createSession    func(ctx context.Context, options ...agentopt.Option) (*memory.Session, error)
	marshalSession   func(ctx context.Context, session *memory.Session) ([]byte, error)
	unmarshalSession func(ctx context.Context, data []byte) (*memory.Session, error)
	run              func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error]
}

func (a *Agent) ID() string {
	if a == nil {
		return ""
	}
	return a.id
}

func (a *Agent) Name() string {
	if a == nil {
		return ""
	}
	return a.name
}

func (a *Agent) Description() string {
	if a == nil {
		return ""
	}
	return a.description
}

func (a *Agent) ProviderName() string {
	if a == nil {
		return ""
	}
	return a.providerName
}

func (a *Agent) CreateSession(ctx context.Context, options ...agentopt.Option) (*memory.Session, error) {
	if a.createSession == nil {
		session := memory.NewSession("")
		session.ServiceID, _ = agentopt.Get(options, agentopt.ServiceID)
		return session, nil
	}
	return a.createSession(ctx, options...)
}

func (a *Agent) MarshalSession(ctx context.Context, session *memory.Session) ([]byte, error) {
	if a.marshalSession == nil {
		if session == nil {
			return nil, errors.New("the provided session is nil")
		}
		return json.Marshal(session)
	}
	return a.marshalSession(ctx, session)
}

func (a *Agent) UnmarshalSession(ctx context.Context, data []byte) (*memory.Session, error) {
	if a.unmarshalSession == nil {
		var session memory.Session
		if err := json.Unmarshal(data, &session); err != nil {
			return nil, err
		}
		return &session, nil
	}
	return a.unmarshalSession(ctx, data)
}

func (a *Agent) RunText(ctx context.Context, msg string, options ...agentopt.Option) ResponseStream {
	return a.Run(ctx, []*message.Message{message.NewText(msg)}, options...)
}

func (a *Agent) RunMessage(ctx context.Context, msg *message.Message, options ...agentopt.Option) ResponseStream {
	return a.Run(ctx, []*message.Message{msg}, options...)
}

func (a *Agent) Run(ctx context.Context, messages []*message.Message, options ...agentopt.Option) ResponseStream {
	ctx, preparedMessages, options, err := a.prepareRun(ctx, messages, options)
	if err != nil {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	return ResponseStream(middleware.RunChain(ctx, a.run, a.middlewares, preparedMessages, options...))
}

func (a *Agent) prepareRun(ctx context.Context, messages []*message.Message, options []agentopt.Option) (context.Context, []*message.Message, []agentopt.Option, error) {
	// Prepend options from agent configuration.
	if len(a.runOptions) != 0 {
		options = append(a.runOptions, options...)
	}

	if _, ok := agentopt.Get(options, agentopt.Session); !ok {
		if allowBackgroundResponses, ok := agentopt.Get(options, agentopt.AllowBackgroundResponses); ok && allowBackgroundResponses {
			// Background responses require an explicit session to avoid inconsistent
			// caller experience between initial and follow-up runs.
			return nil, nil, nil, errors.New("a session must be provided when AllowBackgroundResponses is enabled")
		}
		// Ensure a session is provided in the options.
		session, err := a.CreateSession(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		options = append(options, agentopt.Session(session), noSessionProvided(true))
	}

	continuationToken, _ := agentopt.Get(options, agentopt.ContinuationToken)
	if continuationToken != "" && len(messages) > 0 {
		return nil, nil, nil, errors.New("messages are not allowed when continuing a background response using a continuation token")
	}
	if continuationToken == "" {
		if a.instructions != "" {
			preparedMessages := make([]*message.Message, 0, len(messages)+1)
			preparedMessages = append(preparedMessages, &message.Message{
				Role: message.RoleSystem,
				Contents: []message.Content{
					&message.TextContent{Text: a.instructions},
				},
			})
			preparedMessages = append(preparedMessages, messages...)
			messages = preparedMessages
		}
	}

	// Add agent identity to context so that middlewares can log it.
	ctx = context.WithValue(ctx, agentKey{}, a)

	return ctx, messages, options, nil
}

func providerMiddleware(contextProviders []*memory.ContextProvider) middleware.Middleware {
	return middleware.Func(func(next middleware.RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		session, _ := agentopt.Get(options, agentopt.Session)
		noSession, _ := agentopt.Get(options, noSessionProvided)
		contToken, _ := agentopt.Get(options, agentopt.ContinuationToken)

		activeProviders := contextProviders
		if !noSession && contToken == "" && session.ServiceID == "" && len(activeProviders) == 0 {
			// For local sessions without explicit providers, attach in-memory history at runtime.
			activeProviders = []*memory.ContextProvider{memory.NewInMemoryHistoryProvider("in-memory")}
		}

		return func(yield func(*message.ResponseUpdate, error) bool) {
			// Retrieve provider context.
			currentMessages := messages
			currentTools := slices.Collect(agentopt.All(options, agentopt.Tool))
			runOptions := options
			for _, contextProvider := range activeProviders {
				if contextProvider == nil {
					continue
				}
				providerContext, err := contextProvider.BeforeRun(memory.BeforeRunContext{
					Context:  ctx,
					Session:  session,
					Messages: currentMessages,
					Tools:    currentTools,
				})
				if err != nil {
					yield(nil, err)
					return
				}
				if len(providerContext.Messages) > 0 {
					merged := make([]*message.Message, 0, len(providerContext.Messages)+len(currentMessages))
					merged = append(merged, providerContext.Messages...)
					merged = append(merged, currentMessages...)
					currentMessages = merged
				}
				if len(providerContext.Tools) > 0 {
					currentTools = append(currentTools, providerContext.Tools...)
					for _, tool := range providerContext.Tools {
						runOptions = append(runOptions, agentopt.Tool(tool))
					}
				}
			}
			messages = currentMessages
			var resp message.Response
			var runErr error
			for update, err := range next(ctx, messages, runOptions...) {
				if update != nil && session.ServiceID == "" {
					resp.Update(update)
				}
				if !yield(update, err) {
					if err != nil {
						runErr = err
					}
					break
				}
			}
			resp.Coalesce()
			// After the run finishes, persist context.
			for i := len(activeProviders) - 1; i >= 0; i-- {
				contextProvider := activeProviders[i]
				if contextProvider == nil {
					continue
				}
				// Filters may compact slices in-place, so isolate each provider invocation.
				if err := contextProvider.AfterRun(memory.AfterRunContext{
					Context:          ctx,
					Session:          session,
					RequestMessages:  slices.Clone(messages),
					ResponseMessages: slices.Clone(resp.Messages),
					Tools:            currentTools,
					InvokeError:      runErr,
				}); err != nil {
					yield(nil, err)
					return
				}
			}
		}
	})
}

func authorMiddleware(id, name string) middleware.Middleware {
	return middleware.Func(func(next middleware.RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			for update, err := range next(ctx, messages, options...) {
				if update != nil {
					if update.AuthorID == "" {
						update.AuthorID = id
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

func noSessionProvided(v bool) agentopt.Option {
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
