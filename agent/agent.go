// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/message"
)

type Metadata struct {
	ID           string
	Name         string
	Description  string
	ProviderName string
}

type Config struct {
	Metadata Metadata

	RunOptions []agentopt.RunOption

	CreateSession    func(ctx context.Context, options ...agentopt.CreateSessionOption) (memory.Session, error)
	UnmarshalSession func(data []byte) (memory.Session, error)
	Run              func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]
}

func New(cfg Config) *Agent {
	if cfg.Metadata.ID == "" {
		cfg.Metadata.ID = uuid.NewString()
	}
	return &Agent{
		metadata:         cfg.Metadata,
		createSession:    cfg.CreateSession,
		unmarshalSession: cfg.UnmarshalSession,
		run:              cfg.Run,
		runOptions:       cfg.RunOptions,
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
	metadata Metadata

	runOptions []agentopt.RunOption

	createSession    func(ctx context.Context, options ...agentopt.CreateSessionOption) (memory.Session, error)
	unmarshalSession func(data []byte) (memory.Session, error)
	run              func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]
}

func (a *Agent) ID() string {
	return a.metadata.ID
}

func (a *Agent) Name() string {
	return a.metadata.Name
}

func (a *Agent) Metadata() Metadata {
	return a.metadata
}

func (a *Agent) CreateSession(ctx context.Context, options ...agentopt.CreateSessionOption) (memory.Session, error) {
	return a.createSession(ctx, options...)
}

func (a *Agent) UnmarshalSession(data []byte) (memory.Session, error) {
	return a.unmarshalSession(data)
}

func (a *Agent) RunText(msg string, options ...agentopt.RunOption) ResponseStream {
	return a.Run([]*message.Message{message.NewText(msg)}, options...)
}

func (a *Agent) RunMessage(msg *message.Message, options ...agentopt.RunOption) ResponseStream {
	return a.Run([]*message.Message{msg}, options...)
}

func (a *Agent) Run(messages []*message.Message, options ...agentopt.RunOption) ResponseStream {
	return ResponseStream{func(ctx context.Context, stream bool) iter.Seq2[*message.ResponseUpdate, error] {
		ctx, options, err := a.prepareRun(ctx, stream, options)
		if err != nil {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, err)
			}
		}
		return middleware.RunChain(ctx, a.run, messages, options...)
	}}
}

func (a *Agent) prepareRun(ctx context.Context, stream bool, options []agentopt.RunOption) (context.Context, []agentopt.RunOption, error) {
	// Prepend options from agent configuration.
	if len(a.runOptions) != 0 {
		options = append(a.runOptions, options...)
	}

	// Middleware to set AuthorID and AuthorName on each ResponseUpdate.
	options = append(options, middleware.With(middleware.Func(a.authorMiddleware)))

	// Add agent identity to context so that middlewares can log it.
	ctx = context.WithValue(ctx, metadataKey{}, a.Metadata())

	// Ensure a session is provided in the options.
	if _, ok := agentopt.Get(options, agentopt.Session); !ok {
		session, err := a.CreateSession(ctx)
		if err != nil {
			return nil, nil, err
		}
		options = append(options, agentopt.Session(session))
	}

	// If Run.All() is called, set the Stream option to true
	// unless already specified in options.
	if stream {
		if _, ok := agentopt.Get(options, agentopt.Stream); !ok {
			options = append(options, agentopt.Stream(stream))
		}
	}

	return ctx, options, nil
}

func (a *Agent) authorMiddleware(next middleware.RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		id, name := a.ID(), a.Name()
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
}

type metadataKey struct{}

// MetadataFromContext retrieves the agent metadata from the context.
// Returns the metadata and true if found, or zero value and false otherwise.
func MetadataFromContext(ctx context.Context) (Metadata, bool) {
	if v := ctx.Value(metadataKey{}); v != nil {
		return v.(Metadata), true
	}
	return Metadata{}, false
}
