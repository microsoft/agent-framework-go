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
	if prov.FormatOfFn != nil && prov.UnmarshalFn != nil {
		cfg.Middlewares = append(cfg.Middlewares,
			structuredoutput.New(structuredoutput.Config{
				Format:    prov.FormatOfFn,
				Unmarshal: prov.UnmarshalFn,
			}),
		)
	}
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
type ResponseStream struct {
	run func(ctx context.Context, stream bool) iter.Seq2[*message.ResponseUpdate, error]
}

// All returns an iterator over all response updates from the agent run.
// Streaming will be automatically enabled unless [agentopt.Stream]
// is explicitly set in the run options.
func (r ResponseStream) All(ctx context.Context) iter.Seq2[*message.ResponseUpdate, error] {
	return r.run(ctx, true)
}

// Collect gathers all response updates into a single Response object.
func (r ResponseStream) Collect(ctx context.Context) (*message.Response, error) {
	var resp message.Response
	for update, err := range r.run(ctx, false) {
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

func (a *Agent) RunText(msg string, options ...agentopt.Option) ResponseStream {
	return a.Run([]*message.Message{message.NewText(msg)}, options...)
}

func (a *Agent) RunMessage(msg *message.Message, options ...agentopt.Option) ResponseStream {
	return a.Run([]*message.Message{msg}, options...)
}

func (a *Agent) Run(messages []*message.Message, options ...agentopt.Option) ResponseStream {
	return ResponseStream{func(ctx context.Context, stream bool) iter.Seq2[*message.ResponseUpdate, error] {
		ctx, preparedMessages, options, err := a.prepareRun(ctx, messages, stream, options)
		if err != nil {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, err)
			}
		}
		return middleware.RunChain(ctx, a.run, a.middlewares, preparedMessages, options...)
	}}
}

func (a *Agent) prepareRun(ctx context.Context, messages []*message.Message, stream bool, options []agentopt.Option) (context.Context, []*message.Message, []agentopt.Option, error) {
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
		options = append(options, agentopt.Session(session))
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

	// If Run.All() is called, set the Stream option to true
	// unless already specified in options.
	if stream {
		if _, ok := agentopt.Get(options, agentopt.Stream); !ok {
			options = append(options, agentopt.Stream(stream))
		}
	}

	return ctx, messages, options, nil
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

// AgentFromContext retrieves the agent that initiated the run from the context.
// Returns the agent and true if found, or nil and false otherwise.
func AgentFromContext(ctx context.Context) (*Agent, bool) {
	if v := ctx.Value(agentKey{}); v != nil {
		return v.(*Agent), true
	}
	return nil, false
}
