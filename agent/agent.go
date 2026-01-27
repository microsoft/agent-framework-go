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

type Agent interface {
	ID() string
	Name() string
	Description() string

	Run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]

	NewSession(ctx context.Context, options ...agentopt.NewSessionOption) (memory.Session, error)
	UnmarshalSession(data []byte) (memory.Session, error)

	internal() // unexported method to prevent external implementations
}

type Config struct {
	ID          string
	Name        string
	Description string

	RunOptions []agentopt.RunOption

	NewSession       func(ctx context.Context, options ...agentopt.NewSessionOption) (memory.Session, error)
	UnmarshalSession func(data []byte) (memory.Session, error)
	Run              func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]
}

func New(cfg Config) Agent {
	return &agent{
		id:               cfg.ID,
		name:             cfg.Name,
		description:      cfg.Description,
		newSession:       cfg.NewSession,
		unmarshalSession: cfg.UnmarshalSession,
		run:              cfg.Run,
		runOptions:       cfg.RunOptions,
	}
}

type agent struct {
	id          string
	name        string
	description string

	runOptions []agentopt.RunOption

	newSession       func(ctx context.Context, options ...agentopt.NewSessionOption) (memory.Session, error)
	unmarshalSession func(data []byte) (memory.Session, error)
	run              func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]
}

func (a *agent) ID() string {
	if a.id == "" {
		a.id = uuid.NewString()
	}
	return a.id
}

func (a *agent) Name() string {
	return a.name
}

func (a *agent) Description() string {
	return a.description
}

func (a *agent) Run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	if len(a.runOptions) != 0 {
		// Prepend options from agent configuration.
		options = append(a.runOptions, options...)
	}
	// Ensure a session is provided in the options.
	if _, ok := agentopt.Get(options, agentopt.Session); !ok {
		session, err := a.NewSession(ctx)
		if err != nil {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, err)
			}
		}
		options = append(options, agentopt.Session(session))
	}
	// Middleware to set AuthorID and AuthorName on each ResponseUpdate.
	options = append(options, middleware.With(middleware.Func(
		func(next middleware.RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
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
		})))

	// Add agent identity to context so that middlewares can log it.
	ctx = context.WithValue(ctx, identityKey{}, identity{
		id:   a.ID(),
		name: a.Name(),
	})
	return middleware.RunChain(ctx, a.run, messages, options...)
}

func (a *agent) NewSession(ctx context.Context, options ...agentopt.NewSessionOption) (memory.Session, error) {
	return a.newSession(ctx, options...)
}

func (a *agent) UnmarshalSession(data []byte) (memory.Session, error) {
	return a.unmarshalSession(data)
}

func (a *agent) internal() {}
