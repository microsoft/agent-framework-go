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

	// MessageHistoryProvider is the provider used for storing and retrieving chat history.
	// If nil, an [memory.InMemoryMessageHistoryProvider] will be used when the underlying
	// service does not manage chat history server-side.
	MessageHistoryProvider memory.ContextProvider

	// ContextProviders is the list of context providers used for providing additional context
	// for each agent run. Multiple providers form a pipeline where each provider can build on
	// top of the previous provider's context.
	ContextProviders []memory.ContextProvider
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

	messageHistoryProvider memory.ContextProvider
	contextProviders       []memory.ContextProvider
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
		runFn:                  runfn,
		instructions:           opts.Instructions,
		messageHistoryProvider: opts.MessageHistoryProvider,
		contextProviders:       opts.ContextProviders,
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
		originalMessages := messages
		session, options, messages, err := a.prepareSessionAndMessages(ctx, originalMessages, options)
		if err != nil {
			yield(nil, err)
			return
		}
		contToken, _ := agentopt.Get(options, agentopt.ContinuationToken)
		if err := validateStreamResumptionAllowed(contToken, session, a.contextProviders); err != nil {
			yield(nil, err)
			return
		}
		var resp message.Response
		for update, err := range a.runFn(ctx, messages, options...) {
			if err != nil {
				a.notifyContextProvidersOfFailure(ctx, session, err, messages)
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
		// Notify the message history provider of new messages if the response was successful.
		if err := a.notifyMessageHistoryProvider(ctx, session, originalMessages, resp.Messages); err != nil {
			yield(nil, err)
			return
		}
		// Notify the context providers of all new messages.
		if err := a.notifyContextProvidersOfSuccess(ctx, session, messages, resp.Messages); err != nil {
			yield(nil, err)
			return
		}
	}
}

func (a *chatagent) notifyMessageHistoryProvider(ctx context.Context, session *Session, requestMessages, responseMessages []*message.Message) error {
	if session.ConversationID != "" {
		// If the session messages are stored in the service
		// there is nothing to do here.
		return nil
	}
	provider := a.messageHistoryProvider
	if provider == nil {
		// If there is no message history provider, use a default in-memory one
		// scoped to this invocation instead of caching it on the agent.
		provider = &memory.InMemoryMessageHistoryProvider{}
	}
	return provider.Invoked(&memory.InvokedContext{
		Context:          ctx,
		RequestMessages:  requestMessages,
		ResponseMessages: responseMessages,
		Session:          session,
	})
}

func (a *chatagent) notifyContextProvidersOfSuccess(ctx context.Context, session *Session, messages, respMessages []*message.Message) error {
	for _, provider := range a.contextProviders {
		if err := provider.Invoked(&memory.InvokedContext{
			Context:          ctx,
			RequestMessages:  messages,
			ResponseMessages: respMessages,
			Session:          session,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *chatagent) notifyContextProvidersOfFailure(ctx context.Context, session *Session, err error, messages []*message.Message) {
	for _, provider := range a.contextProviders {
		_ = provider.Invoked(&memory.InvokedContext{
			Context:         ctx,
			InvokeError:     err,
			RequestMessages: messages,
			Session:         session,
		})
	}
}

func (a *chatagent) prepareSessionAndMessages(ctx context.Context, messages []*message.Message, options []agentopt.RunOption) (session *Session, opts []agentopt.RunOption, msgsForClient []*message.Message, err error) {
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
		if session.ConversationID == "" && a.messageHistoryProvider == nil {
			return retError(errors.New("continuation tokens are not allowed to be used for initial runs"))
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
		if session.ConversationID == "" && a.messageHistoryProvider != nil {
			// Add any existing messages from the session to the messages to be sent to the chat client.
			// Only when the service is not managing the chat history (no ConversationID).
			newCtx, err := a.messageHistoryProvider.Invoking(&memory.InvokingContext{
				Context:  ctx,
				Messages: messages,
				Session:  session,
			})
			if err != nil {
				return retError(err)
			}
			if newCtx != nil {
				msgsForClient = append(msgsForClient, newCtx.Messages...)
			}
		}
		if len(a.contextProviders) > 0 {
			// If we have context providers, call each one in sequence to build up the context.
			// Each provider receives the accumulated context from previous providers.
			accContext := &memory.Context{
				Messages: append(slices.Clone(msgsForClient), messages...),
			}
			for _, provider := range a.contextProviders {
				ctxData, err := provider.Invoking(&memory.InvokingContext{
					Context:    ctx,
					Messages:   messages,
					AccContext: accContext,
					Session:    session,
				})
				if err != nil {
					return retError(err)
				}
				if ctxData != nil {
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
					// Accumulate instructions and tools from all providers.
					newInstructions := accContext.Instructions
					if ctxData.Instructions != "" {
						if newInstructions != "" {
							newInstructions += "\n"
						}
						newInstructions += ctxData.Instructions
					}
					newTools := slices.Clone(accContext.Tools)
					newTools = append(newTools, ctxData.Tools...)
					accContext = &memory.Context{
						Instructions: newInstructions,
						Messages:     append(slices.Clone(msgsForClient), messages...),
						Tools:        newTools,
					}
				}
			}
		}
		// Add the input messages to the end of session messages.
		msgsForClient = append(msgsForClient, messages...)
	}
	return session, options, msgsForClient, nil
}

func validateStreamResumptionAllowed(continuationToken string, session *Session, contextProviders []memory.ContextProvider) error {
	if continuationToken == "" {
		return nil
	}
	// Streaming resumption is only supported with chat history managed by the agent service because, currently, there's no good solution
	// to collect updates received in failed runs and pass them to the last successful run so it can store them to the message store.
	if session.ConversationID == "" {
		return errors.New("streaming resumption is only supported when chat history is stored and managed by the underlying service")
	}
	// Similarly, streaming resumption is not supported when context providers are used because, currently, there's no good solution
	// to collect updates received in failed runs and pass them to the last successful run so it can notify the context providers of the updates.
	if len(contextProviders) > 0 {
		return errors.New("using context providers with streaming resumption is not supported")
	}
	return nil
}
