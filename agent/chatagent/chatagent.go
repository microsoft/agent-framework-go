// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"cmp"
	"context"
	"errors"
	"iter"
	"log/slog"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/agent/middleware/autocall"
	"github.com/microsoft/agent-framework-go/agent/middleware/logger"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
)

type Config struct {
	ID          string
	Name        string
	Description string

	Instructions string

	Logger           *slog.Logger
	LogSensitiveData bool

	FormatOfFn  func(v any) (format.Format, error)
	UnmarshalFn func(format format.Format, data []byte, v any) error

	DisableFuncAutoCall bool
	Middlewares         []middleware.Middleware

	RunOptions []agentopt.RunOption

	NewMessageStore    func() memory.MessageStore
	NewContextProvider func() memory.ContextProvider
}

func (o *Config) Clone() *Config {
	if o == nil {
		return nil
	}
	clone := *o
	clone.Middlewares = slices.Clone(o.Middlewares)
	clone.RunOptions = slices.Clone(o.RunOptions)
	return &clone
}

type RunFunc func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]

var _ agent.Agent = (*Agent)(nil)

type Agent struct {
	runFn  RunFunc
	config Config
	iden   agent.Identity
}

// NewAgent creates a new chat agent with the given chat client and options.
func NewAgent(runfn RunFunc, cfg Config) *Agent {
	opts := *cfg.Clone()
	if !opts.DisableFuncAutoCall {
		opts.Middlewares = append(opts.Middlewares,
			autocall.New(autocall.Config{
				Logger:           opts.Logger,
				LogSensitiveData: opts.LogSensitiveData,
			}),
		)
	}
	return &Agent{
		runFn:  runfn,
		config: opts,
		iden:   agent.NewIdentity(cfg.ID, cfg.Name, cfg.Description),
	}
}

func (a *Agent) Identity() agent.Identity {
	return a.iden
}

func (a *Agent) Instructions() string {
	return a.config.Instructions
}

func (a *Agent) NewThread(ctx context.Context, opts ...agentopt.NewThreadOption) memory.Thread {
	convID, _ := agentopt.Get(opts, ConversationID)
	thread := &Thread{
		ConversationID: convID,
	}
	if a.config.NewContextProvider != nil {
		thread.ContextProvider = a.config.NewContextProvider()
	}
	return thread
}

func (a *Agent) UnmarshalThread(data []byte) (memory.Thread, error) {
	return newThreadFromJSON(data, a.config.NewMessageStore, a.config.NewContextProvider)
}

func (a *Agent) Run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	// Prepend options from agent configuration.
	options = append(a.config.RunOptions, options...)
	if _, ok := agentopt.Get(options, agentopt.Thread); !ok {
		options = append(options, agentopt.Thread(a.NewThread(ctx)))
	}
	ctx = logger.WithAgentContext(ctx, a.iden.ID(), a.iden.Name())
	return middleware.RunChain(ctx, a.run, a.config.Middlewares, messages, options...)
}

func (a *Agent) RunOf(ctx context.Context, v any, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		if a.config.FormatOfFn == nil || a.config.UnmarshalFn == nil {
			yield(nil, errors.New("agent does not support structured output"))
			return
		}
		format, err := a.config.FormatOfFn(v)
		if err != nil {
			yield(nil, err)
			return
		}
		options = append(options, agentopt.ResponseFormat(format))
		var data []byte
		for update, err := range a.Run(ctx, messages, options...) {
			if err != nil {
				yield(nil, err)
				return
			}
			data = append(data, update.String()...)
		}
		if err := a.config.UnmarshalFn(format, data, v); err != nil {
			yield(nil, err)
			return
		}
	}
}

func (a *Agent) run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		originalMessages := messages
		thread, options, messages, ctxMessages, err := a.prepareThreadAndMessages(ctx, originalMessages, options)
		if err != nil {
			yield(nil, err)
			return
		}
		contToken, _ := agentopt.Get(options, agentopt.ContinuationToken)
		if err := validateStreamResumptionAllowed(contToken, thread); err != nil {
			yield(nil, err)
			return
		}
		var resp message.Response
		for update, err := range a.runFn(ctx, messages, options...) {
			if err != nil {
				a.notifyContextProviderOfFailure(ctx, thread, err, messages, ctxMessages)
				yield(nil, err)
				return
			}
			if update != nil {
				update.AuthorName = cmp.Or(update.AuthorName, a.Identity().Name())
				resp.Update(update)
				if !yield(&message.ResponseUpdate{
					AuthorID:             a.Identity().ID(),
					AuthorName:           update.AuthorName,
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
		if thread.ConversationID == "" && thread.MessageStore == nil && a.config.NewMessageStore != nil {
			// If we don't have a conversation ID then we must be managing the message store ourselves.
			// If we don't have a message store yet and we have a factory, use it to create a new one.
			thread.MessageStore = a.config.NewMessageStore()
		}
		// Only notify the thread of new messages if the response was successful to avoid inconsistent message state in the thread.
		if err := thread.MessagesReceived(ctx, append(originalMessages, append(ctxMessages, resp.Messages...)...)...); err != nil {
			yield(nil, err)
			return
		}
		// Notify the ContextProvider of all new messages.
		if err := a.notifyContextProviderOfSuccess(ctx, thread, messages, ctxMessages, resp.Messages); err != nil {
			yield(nil, err)
			return
		}
	}
}

func (a *Agent) notifyContextProviderOfSuccess(ctx context.Context, thread *Thread, messages, contextProviderMessages, respMessages []*message.Message) error {
	if thread.ContextProvider == nil {
		return nil
	}
	return thread.ContextProvider.Invoked(&memory.InvokedContext{
		Context:                 ctx,
		RequestMessages:         messages,
		ContextProviderMessages: contextProviderMessages,
		ResponsesMessages:       respMessages,
	})
}

func (a *Agent) notifyContextProviderOfFailure(ctx context.Context, thread *Thread, err error, messages, contextProviderMessages []*message.Message) error {
	if thread.ContextProvider == nil {
		return nil
	}
	return thread.ContextProvider.Invoked(&memory.InvokedContext{
		Context:                 ctx,
		Error:                   err,
		RequestMessages:         messages,
		ContextProviderMessages: contextProviderMessages,
	})
}

func (a *Agent) prepareThreadAndMessages(ctx context.Context, messages []*message.Message, options []agentopt.RunOption) (thread *Thread, opts []agentopt.RunOption, msgsForClient, ctxMessages []*message.Message, err error) {
	retError := func(e error) (*Thread, []agentopt.RunOption, []*message.Message, []*message.Message, error) {
		return nil, nil, nil, nil, e
	}
	if v, ok := agentopt.Get(options, agentopt.AllowBackgroundResponses); ok && v && thread == nil {
		return retError(errors.New("a thread must be provided when continuing a background response with a continuation token"))
	}
	if v, ok := agentopt.Get(options, agentopt.Thread); ok {
		var ok bool
		thread, ok = v.(*Thread)
		if !ok {
			return retError(errors.New("the provided thread is not compatible with the agent, only threads created by the agent can be used"))
		}
	} else {
		// This should never happen because we ensure a thread is always provided in Run.
		panic("nil thread")
	}
	if v, ok := agentopt.Get(options, agentopt.ContinuationToken); ok && v != nil {
		if len(messages) > 0 {
			return retError(errors.New("messages are not allowed when continuing a background response using a continuation token"))
		}
		if thread.ConversationID == "" && thread.MessageStore == nil {
			return retError(errors.New("continuation tokens are not allowed to be used for initial runs"))
		}
	}

	if a.Instructions() != "" {
		msgsForClient = append(msgsForClient, &message.Message{
			Role: message.RoleSystem,
			Contents: []message.Content{
				&message.TextContent{
					Text: a.Instructions(),
				},
			},
		})
	}
	if v, ok := agentopt.Get(options, agentopt.ContinuationToken); !ok || v == nil {
		if thread.MessageStore != nil {
			//  Add any existing messages from the thread to the messages to be sent to the chat client.
			for msg, err := range thread.MessageStore.All(ctx) {
				if err != nil {
					return retError(err)
				}
				msgsForClient = append(msgsForClient, msg)
			}
		}
		if thread.ContextProvider != nil {
			// If we have a ContextProvider, we should get context from it, and update our
			// messages and options with the additional context.
			ctxData, err := thread.ContextProvider.Invoking(&memory.InvokingContext{
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
		// Add the input messages to the end of thread messages.
		msgsForClient = append(msgsForClient, messages...)
	}
	return thread, options, msgsForClient, ctxMessages, nil
}

func validateStreamResumptionAllowed(continuationToken any, thread *Thread) error {
	if continuationToken == nil {
		return nil
	}
	// Streaming resumption is only supported with chat history managed by the agent service because, currently, there's no good solution
	// to collect updates received in failed runs and pass them to the last successful run so it can store them to the message store.
	if thread.ConversationID == "" {
		return errors.New("streaming resumption is only supported when chat history is stored and managed by the underlying service")
	}
	// Similarly, streaming resumption is not supported when a context provider is used because, currently, there's no good solution
	// to collect updates received in failed runs and pass them to the last successful run so it can notify the context provider of the updates.
	if thread.ContextProvider != nil {
		return errors.New("using context provider with streaming resumption is not supported")
	}
	return nil
}
