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

	instructions string
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
		runFn:        runfn,
		instructions: opts.Instructions,
	}
	return agent.New(agent.Config{
		Metadata: agent.Metadata{
			ID:           cfg.ID,
			Name:         cfg.Name,
			ProviderName: prov.Name,
			Description:  cfg.Description,
		},

		CreateSession:    a.createSession,
		MarshalSession:   a.marshalSession,
		UnmarshalSession: a.unmarshalSession,
		Run:              a.run,

		RunOptions: opts.RunOptions,
	})
}

func (a *chatagent) createSession(ctx context.Context, opts ...agentopt.CreateSessionOption) (memory.Session, error) {
	convID, _ := agentopt.Get(opts, ConversationID)
	session := &Session{
		ConversationID: convID,
	}
	return session, nil
}

func (a *chatagent) marshalSession(_ context.Context, session memory.Session) ([]byte, error) {
	if _, ok := session.(*Session); !ok {
		return nil, errors.New("the provided session is not compatible with the agent, only sessions created by the agent can be used")
	}
	return json.Marshal(session)
}

func (a *chatagent) unmarshalSession(_ context.Context, data []byte) (memory.Session, error) {
	return newSessionFromJSON(data)
}

func (a *chatagent) run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		session, options, messages, err := a.prepareSessionAndMessages(messages, options)
		if err != nil {
			yield(nil, err)
			return
		}
		contToken, _ := agentopt.Get(options, agentopt.ContinuationToken)
		if err := validateStreamResumptionAllowed(contToken, session); err != nil {
			yield(nil, err)
			return
		}
		var resp message.Response
		for update, err := range a.runFn(ctx, messages, options...) {
			if err != nil {
				yield(nil, err)
				return
			}
			if update != nil {
				resp.Update(update)
				if !yield(&message.ResponseUpdate{
					MessageID:            update.MessageID,
					ResponseID:           update.ResponseID,
					CreatedAt:            update.CreatedAt,
					Role:                 update.Role,
					ContinuationToken:    update.ContinuationToken,
					Contents:             update.Contents,
					RawRepresentation:    update.RawRepresentation,
					AdditionalProperties: update.AdditionalProperties,
				}, nil) {
					return
				}
			}
		}
		resp.Coalesce()
	}
}

func (a *chatagent) prepareSessionAndMessages(messages []*message.Message, options []agentopt.RunOption) (session *Session, opts []agentopt.RunOption, msgsForClient []*message.Message, err error) {
	retError := func(e error) (*Session, []agentopt.RunOption, []*message.Message, error) {
		return nil, nil, nil, e
	}
	if v, ok := agentopt.Get(options, agentopt.Session); ok {
		var ok bool
		session, ok = v.(*Session)
		if !ok {
			return retError(errors.New("the provided session is not compatible with the agent, only sessions created by the agent can be used"))
		}
	} else {
		// This should never happen because we ensure a session is always provided in Run.
		panic("nil session")
	}
	// Now check if AllowBackgroundResponses requires a session
	if v, ok := agentopt.Get(options, agentopt.AllowBackgroundResponses); ok && v && session == nil {
		return retError(errors.New("a session must be provided when continuing a background response with a continuation token"))
	}
	if v, ok := agentopt.Get(options, agentopt.ContinuationToken); ok && v != "" {
		if len(messages) > 0 {
			return retError(errors.New("messages are not allowed when continuing a background response using a continuation token"))
		}
	}

	if v, ok := agentopt.Get(options, agentopt.ContinuationToken); !ok || v == "" {
		if a.instructions != "" {
			msgsForClient = append(msgsForClient, &message.Message{
				Role: message.RoleSystem,
				Contents: []message.Content{
					&message.TextContent{
						Text: a.instructions,
					},
				},
			})
		}
		// Add the input messages to the end of session messages.
		msgsForClient = append(msgsForClient, messages...)
	}
	return session, options, msgsForClient, nil
}

func validateStreamResumptionAllowed(continuationToken string, session *Session) error {
	if continuationToken == "" {
		return nil
	}
	// Streaming resumption is only supported with chat history managed by the agent service because, currently, there's no good solution
	// to collect updates received in failed runs and pass them to the last successful run so it can store them to the message store.
	if session.ConversationID == "" {
		return errors.New("streaming resumption is only supported when chat history is stored and managed by the underlying service")
	}
	return nil
}
