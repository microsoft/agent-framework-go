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
	"github.com/microsoft/agent-framework-go/agent/internal/middleware/autocall"
	"github.com/microsoft/agent-framework-go/agent/internal/middleware/structuredoutput"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

// RunFunc is the provider function that executes an agent invocation.
type RunFunc = func(ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*message.ResponseUpdate, error]

// ProviderConfig configures the provider-specific implementation behind an Agent.
type ProviderConfig struct {
	// ProviderName identifies the underlying provider implementation.
	ProviderName string

	// Run executes a request and streams response updates.
	Run RunFunc

	// CreateSession creates a provider-specific session.
	CreateSession func(ctx context.Context, options ...Option) (*Session, error)
	// MarshalSession serializes a provider-specific session.
	MarshalSession func(ctx context.Context, session *Session) ([]byte, error)
	// UnmarshalSession deserializes a provider-specific session.
	UnmarshalSession func(ctx context.Context, data []byte) (*Session, error)
	// FormatOfFn returns the response format for a structured output destination.
	FormatOfFn func(v any) (format.Format, error)
	// UnmarshalFn unmarshals provider output into a structured output destination.
	UnmarshalFn func(format format.Format, data []byte, v any) error
}

// Config configures an Agent instance.
type Config struct {
	// ID uniquely identifies the agent. A random UUID is assigned when empty.
	ID string
	// Name is the display name used for agent-authored messages.
	Name string
	// Description describes the agent's purpose.
	Description string

	// Instructions are prepended as a system message for each non-continuation run.
	Instructions string

	// ContextProviders inject and persist context around each agent run.
	ContextProviders []*ContextProvider

	// DisableFuncAutoCall disables automatic function-tool calling middleware.
	DisableFuncAutoCall bool

	// Logger receives middleware and provider diagnostics.
	Logger *slog.Logger
	// LogSensitiveData enables logging of sensitive request and response payloads.
	LogSensitiveData bool

	// Middlewares wrap the provider run function.
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
	cfg.Tools = slices.Clone(cfg.Tools)
	for _, tool := range cfg.Tools {
		if tool != nil {
			cfg.RunOptions = append(cfg.RunOptions, WithTool(tool))
		}
	}
	cfg.Middlewares = slices.Clone(cfg.Middlewares)
	if !cfg.DisableFuncAutoCall {
		cfg.Middlewares = append(cfg.Middlewares,
			autocall.New(autocall.Config{
				Logger:           cfg.Logger,
				LogSensitiveData: cfg.LogSensitiveData,
			}),
		)
	}
	providers := make([]*ContextProvider, 0, len(cfg.ContextProviders)+1)
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
	prefixedMiddlewares := make([]Middleware, 0, 2)
	if len(providers) == 0 {
		prefixedMiddlewares = append(prefixedMiddlewares, defaultLocalHistoryMiddleware())
	}
	if len(providers) > 0 {
		prefixedMiddlewares = append(prefixedMiddlewares, newContextProviderMiddleware(providers...))
	}
	cfg.Middlewares = append(prefixedMiddlewares, cfg.Middlewares...)
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

// Agent coordinates message preparation, middleware, sessions, and provider execution.
type Agent struct {
	id           string
	name         string
	description  string
	providerName string

	instructions string

	middlewares []Middleware
	runOptions  []Option

	createSession    func(ctx context.Context, options ...Option) (*Session, error)
	marshalSession   func(ctx context.Context, session *Session) ([]byte, error)
	unmarshalSession func(ctx context.Context, data []byte) (*Session, error)
	run              func(ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*message.ResponseUpdate, error]
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
	return a.providerName
}

// CreateSession creates a session for this agent.
func (a *Agent) CreateSession(ctx context.Context, options ...Option) (*Session, error) {
	if a.createSession == nil {
		session := NewSession("")
		session.ServiceID, _ = GetOption(options, WithServiceID)
		return session, nil
	}
	return a.createSession(ctx, options...)
}

// MarshalSession serializes a session created for this agent.
func (a *Agent) MarshalSession(ctx context.Context, session *Session) ([]byte, error) {
	if a.marshalSession == nil {
		if session == nil {
			return nil, errors.New("the provided session is nil")
		}
		return json.Marshal(session)
	}
	return a.marshalSession(ctx, session)
}

// UnmarshalSession deserializes a session for this agent.
func (a *Agent) UnmarshalSession(ctx context.Context, data []byte) (*Session, error) {
	if a.unmarshalSession == nil {
		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			return nil, err
		}
		return &session, nil
	}
	return a.unmarshalSession(ctx, data)
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
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	return ResponseStream(runChain(ctx, a.run, a.middlewares, preparedMessages, options...))
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
		session, err := a.CreateSession(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		options = append(options, WithSession(session), noSessionProvided(true))
	}

	continuationToken, _ := GetOption(options, WithContinuationToken)
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

// defaultLocalHistoryMiddleware attaches an in-memory history provider for local
// sessions so multi-turn runs keep prior messages without requiring explicit
// configuration. New prepends it only when Config.ContextProviders is empty.
// It is bypassed for auto-created sessions on the first run, service-managed
// sessions, and continuation-token resumes.
func defaultLocalHistoryMiddleware() Middleware {
	history := newContextProviderMiddleware(NewInMemoryHistoryProvider(""))

	return MiddlewareFunc(func(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*message.ResponseUpdate, error] {
		session, _ := GetOption(options, WithSession)
		noSession, _ := GetOption(options, noSessionProvided)
		contToken, _ := GetOption(options, WithContinuationToken)
		if noSession || contToken != "" || session.ServiceID != "" {
			return next(ctx, messages, options...)
		}
		return history.Run(next, ctx, messages, options...)
	})
}

func authorMiddleware(id, name string) Middleware {
	return MiddlewareFunc(func(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*message.ResponseUpdate, error] {
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
