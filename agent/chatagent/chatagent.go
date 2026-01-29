// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"context"
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

	NewMessageHistoryProvider func() memory.ContextProvider
	NewContextProvider        func() memory.ContextProvider
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

	newMessageHistoryProvider func() memory.ContextProvider
	newContextProvider        func() memory.ContextProvider
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
		runFn:                     runfn,
		instructions:              opts.Instructions,
		newMessageHistoryProvider: opts.NewMessageHistoryProvider,
		newContextProvider:        opts.NewContextProvider,
	}
	return agent.New(agent.Config{
		Metadata: agent.Metadata{
			ID:           cfg.ID,
			Name:         cfg.Name,
			ProviderName: prov.Name,
			Description:  cfg.Description,
		},

		NewSession:       a.NewSession,
		UnmarshalSession: a.UnmarshalSession,
		Run:              a.Run,

		RunOptions: opts.RunOptions,
	})
}

func (a *chatagent) NewSession(ctx context.Context, opts ...agentopt.NewSessionOption) (memory.Session, error) {
	convID, _ := agentopt.Get(opts, ConversationID)
	session := &Session{
		ConversationID: convID,
	}
	if a.newContextProvider != nil {
		session.ContextProvider = a.newContextProvider()
	}
	return session, nil
}

func (a *chatagent) UnmarshalSession(data []byte) (memory.Session, error) {
	return newSessionFromJSON(data, a.newMessageHistoryProvider, a.newContextProvider)
}

func (a *chatagent) Run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		originalMessages := messages
		session, options, messages, ctxMessages, err := prepareSessionAndMessages(ctx, a.instructions, originalMessages, options)
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
				notifyContextProviderOfFailure(ctx, session, err, messages, ctxMessages)
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
		if session.ConversationID == "" && session.MessageHistoryProvider == nil && a.newMessageHistoryProvider != nil {
			// If we don't have a conversation ID then we must be managing the message history provider ourselves.
			// If we don't have a message history provider yet and we have a factory, use it to create a new one.
			session.MessageHistoryProvider = a.newMessageHistoryProvider()
		}
		// Only notify the session of new messages if the response was successful to avoid inconsistent message state in the session.
		if err := session.messagesReceived(&memory.InvokedContext{
			Context:                 ctx,
			RequestMessages:         originalMessages,
			ContextProviderMessages: ctxMessages,
			ResponsesMessages:       resp.Messages,
		}); err != nil {
			yield(nil, err)
			return
		}
		// Notify the ContextProvider of all new messages.
		if err := notifyContextProviderOfSuccess(ctx, session, messages, ctxMessages, resp.Messages); err != nil {
			yield(nil, err)
			return
		}
	}
}

func notifyContextProviderOfSuccess(ctx context.Context, session *Session, messages, contextProviderMessages, respMessages []*message.Message) error {
	if session.ContextProvider == nil {
		return nil
	}
	return session.ContextProvider.Invoked(&memory.InvokedContext{
		Context:                 ctx,
		RequestMessages:         messages,
		ContextProviderMessages: contextProviderMessages,
		ResponsesMessages:       respMessages,
	})
}

func notifyContextProviderOfFailure(ctx context.Context, session *Session, err error, messages, contextProviderMessages []*message.Message) error {
	if session.ContextProvider == nil {
		return nil
	}
	return session.ContextProvider.Invoked(&memory.InvokedContext{
		Context:                 ctx,
		Error:                   err,
		RequestMessages:         messages,
		ContextProviderMessages: contextProviderMessages,
	})
}

func prepareSessionAndMessages(ctx context.Context, instr string, messages []*message.Message, options []agentopt.RunOption) (session *Session, opts []agentopt.RunOption, msgsForClient, ctxMessages []*message.Message, err error) {
	retError := func(e error) (*Session, []agentopt.RunOption, []*message.Message, []*message.Message, error) {
		return nil, nil, nil, nil, e
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
		if session.ConversationID == "" && session.MessageHistoryProvider == nil {
			return retError(errors.New("continuation tokens are not allowed to be used for initial runs"))
		}
	}

	if instr != "" {
		msgsForClient = append(msgsForClient, &message.Message{
			Role: message.RoleSystem,
			Contents: []message.Content{
				&message.TextContent{
					Text: instr,
				},
			},
		})
	}
	if v, ok := agentopt.Get(options, agentopt.ContinuationToken); !ok || v == "" {
		if session.MessageHistoryProvider != nil {
			//  Add any existing messages from the session to the messages to be sent to the chat client.
			newCtx, err := session.MessageHistoryProvider.Invoking(&memory.InvokingContext{
				Context:  ctx,
				Messages: messages,
			})
			if err != nil {
				return retError(err)
			}
			if newCtx != nil {
				msgsForClient = append(msgsForClient, newCtx.Messages...)
			}
		}
		if session.ContextProvider != nil {
			// If we have a ContextProvider, we should get context from it, and update our
			// messages and options with the additional context.
			ctxData, err := session.ContextProvider.Invoking(&memory.InvokingContext{
				Context:  ctx,
				Messages: messages,
			})
			if err != nil {
				return retError(err)
			}
			if ctxData != nil {
				ctxMessages = ctxData.Messages
				msgsForClient = append(msgsForClient, ctxData.Messages...)
				for _, tl := range ctxData.Tools {
					options = append(options, agentopt.Tool(tl))
				}
				if ctxData.Instructions != "" {
					msgsForClient = append(msgsForClient, &message.Message{
						Role: message.RoleSystem,
						Contents: []message.Content{
							&message.TextContent{
								Text: ctxData.Instructions,
							},
						},
					})
				}
			}
		}
		// Add the input messages to the end of session messages.
		msgsForClient = append(msgsForClient, messages...)
	}
	return session, options, msgsForClient, ctxMessages, nil
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
	// Similarly, streaming resumption is not supported when a context provider is used because, currently, there's no good solution
	// to collect updates received in failed runs and pass them to the last successful run so it can notify the context provider of the updates.
	if session.ContextProvider != nil {
		return errors.New("using context provider with streaming resumption is not supported")
	}
	return nil
}
