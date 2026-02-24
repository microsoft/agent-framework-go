// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/agent/middleware/autocall"
	"github.com/microsoft/agent-framework-go/agent/middleware/structuredoutput"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/message"
)

type Config struct {
	ID          string
	Name        string
	Description string

	Instructions string

	Logger           *slog.Logger
	LogSensitiveData bool

	DisableFuncAutoCall bool

	RunOptions []agentopt.RunOption
}

type ProviderConfig struct {
	Name        string
	FormatOfFn  func(v any) (format.Format, error)
	UnmarshalFn func(format format.Format, data []byte, v any) error
}

func (o *Config) Clone() *Config {
	if o == nil {
		return nil
	}
	clone := *o
	clone.RunOptions = slices.Clone(o.RunOptions)
	return &clone
}

type RunFunc func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]

type chatagent struct {
	runFn RunFunc
}

// NewAgent creates a new chat agent with the given chat client and options.
func NewAgent(runfn RunFunc, cfg Config, prov ProviderConfig) *agent.Agent {
	opts := *cfg.Clone()
	if !opts.DisableFuncAutoCall {
		opts.RunOptions = append(opts.RunOptions, middleware.With(
			autocall.New(autocall.Config{
				Logger:           opts.Logger,
				LogSensitiveData: opts.LogSensitiveData,
			}),
		))
	}
	if prov.FormatOfFn != nil && prov.UnmarshalFn != nil {
		opts.RunOptions = append(opts.RunOptions, middleware.With(
			structuredoutput.New(structuredoutput.Config{
				Format:    prov.FormatOfFn,
				Unmarshal: prov.UnmarshalFn,
			})),
		)
	}
	a := &chatagent{
		runFn: runfn,
	}
	return agent.New(agent.Config{
		Metadata: agent.Metadata{
			ID:           cfg.ID,
			Name:         cfg.Name,
			ProviderName: prov.Name,
			Description:  cfg.Description,
		},

		Instructions: opts.Instructions,

		CreateSession:    a.createSession,
		MarshalSession:   a.marshalSession,
		UnmarshalSession: a.unmarshalSession,
		Run:              a.run,

		RunOptions: opts.RunOptions,
	})
}

func (a *chatagent) createSession(ctx context.Context, opts ...agentopt.CreateSessionOption) (*memory.Session, error) {
	session := memory.NewSession("")
	session.ServiceID, _ = agentopt.Get(opts, agentopt.ServiceID)
	return session, nil
}

func (a *chatagent) marshalSession(_ context.Context, session *memory.Session) ([]byte, error) {
	if session == nil {
		return nil, errors.New("the provided session is nil")
	}
	return json.Marshal(session)
}

func (a *chatagent) unmarshalSession(_ context.Context, data []byte) (*memory.Session, error) {
	var session memory.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (a *chatagent) run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return a.runFn(ctx, messages, options...)
}
