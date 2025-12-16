// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"cmp"
	"context"
	"errors"
	"iter"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/agent/middleware/autocall"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/param"
)

type ChatOptions = chatclient.ChatOptions

var _ agent.Agent = (*Agent)(nil)

type Agent struct {
	Client  chatclient.Client
	Options Options

	iden agent.Identity
}

// NewAgent creates a new chat agent with the given chat client and options.
func NewAgent(client chatclient.Client, options Options) *Agent {
	opts := *options.Clone()
	if !opts.DisableFuncAutoCall {
		opts.Middlewares = append(opts.Middlewares,
			autocall.New(autocall.Options{}),
		)
	}
	return &Agent{
		Client:  client,
		Options: opts,
		iden:    agent.NewIdentity(options.ID, options.Name, options.Description),
	}
}

func (a *Agent) Identity() agent.Identity {
	return a.iden
}

func (a *Agent) Instructions() string {
	return a.Options.Instructions
}

func (a *Agent) NewThread() memory.Thread {
	return a.newThread("")
}

func (a *Agent) NewThreadWithConversationID(conversationID string) *Thread {
	return a.newThread(conversationID)
}

func (a *Agent) newThread(conversationID string) *Thread {
	thread := &Thread{
		ConversationID: conversationID,
	}
	if a.Options.NewContextProvider != nil {
		thread.ContextProvider = a.Options.NewContextProvider()
	}
	return thread
}

func (a *Agent) UnmarshalThread(data []byte) (memory.Thread, error) {
	return newThreadFromJSON(data, a.Options.NewMessageStore, a.Options.NewContextProvider)
}

func (a *Agent) Run(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	if a.Options.ChatOptions != nil {
		for _, tl := range a.Options.ChatOptions.Tools {
			options = append(options, agentopt.Tool(tl))
		}
	}
	if _, ok := agentopt.Get(options, agentopt.Thread); !ok {
		options = append(options, agentopt.Thread(a.NewThread()))
	}
	return middleware.RunChain(ctx, a.run, a.Options.Middlewares, messages, options...)
}

func (a *Agent) RunOf(ctx context.Context, v any, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		formatter := a.Client.Capabilities().StructuredOutput
		if formatter == nil {
			yield(nil, errors.New("agent does not support structured output"))
			return
		}
		format, err := formatter.Format(v)
		if err != nil {
			yield(nil, err)
			return
		}
		options = append(options, agentopt.ResponseFormat(format))
		var personRaw []byte
		for update, err := range a.Run(ctx, messages, options...) {
			if err != nil {
				yield(nil, err)
				return
			}
			personRaw = append(personRaw, update.String()...)
		}
		err = formatter.Unmarshal(personRaw, format, &v)
		if err != nil {
			yield(nil, err)
			return
		}
	}
}

func (a *Agent) run(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		originalMessages := messages
		client := a.Client
		if fn, ok := agentopt.Get(options, WithNewClient); ok {
			// If we have a custom chat client factory, we should use it to create a new chat client with the transformed tools.
			client = fn(client)
		}
		thread, opts, messages, ctxMessages, err := a.prepareThreadAndMessages(ctx, originalMessages, options)
		if err != nil {
			yield(nil, err)
			return
		}
		if err := validateStreamResumptionAllowed(opts.ContinuationToken, thread); err != nil {
			yield(nil, err)
			return
		}
		var updates []*chatclient.ChatResponseUpdate
		for update, err := range client.Response(ctx, opts, messages...) {
			if err != nil {
				a.notifyContextProviderOfFailure(ctx, thread, err, messages, ctxMessages)
				yield(nil, err)
				return
			}
			if update != nil {
				update.AuthorName = cmp.Or(update.AuthorName, a.Identity().Name())
				updates = append(updates, update)
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
		convID := ""
		if len(updates) > 0 {
			convID = updates[len(updates)-1].ConversationID
		}
		// We can derive the type of supported thread from whether we have a conversation id,
		// so let's update it and set the conversation id for the service thread case.
		if err := a.updateThreadWithTypeAndConversationID(thread, convID); err != nil {
			yield(nil, err)
			return
		}
		msgs := chatclient.NewMessageFromUpdates(updates)
		// Only notify the thread of new messages if the chatResponse was successful to avoid inconsistent message state in the thread.
		if err := thread.MessagesReceived(ctx, append(originalMessages, append(ctxMessages, msgs...)...)...); err != nil {
			yield(nil, err)
			return
		}
		// Notify the ContextProvider of all new messages.
		if err := a.notifyContextProviderOfSuccess(ctx, thread, messages, ctxMessages, msgs); err != nil {
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

func (a *Agent) updateThreadWithTypeAndConversationID(thread *Thread, convID string) error {
	if convID == "" && thread.ConversationID != "" {
		// We were passed a thread that is service managed, but we got no conversation id back from the chat client,
		// meaning the service doesn't support service managed threads, so the thread cannot be used with this service.
		return errors.New("service did not return a valid conversation id when using a service managed thread")
	}
	if thread.ConversationID != "" {
		// If we got a conversation id back from the chat client, it means that the service supports server side thread storage
		// so we should update the thread with the new id.
		thread.ConversationID = convID
	} else {
		// If the service doesn't use service side thread storage (i.e. we got no id back from invocation), and
		// the thread has no MessageStore yet, and we have a custom messages store, we should update the thread
		// with the custom MessageStore so that it has somewhere to store the chat history.
		if thread.MessageStore == nil && a.Options.NewMessageStore != nil {
			thread.MessageStore = a.Options.NewMessageStore()
		}
	}
	return nil
}

func (a *Agent) prepareThreadAndMessages(ctx context.Context, messages []*message.Message, options []agentopt.Option) (thread *Thread, opts ChatOptions, msgsForClient, ctxMessages []*message.Message, err error) {
	retError := func(e error) (*Thread, ChatOptions, []*message.Message, []*message.Message, error) {
		return nil, ChatOptions{}, nil, nil, e
	}
	opts = a.createConfiguredChatOptions(options)
	if v, ok := opts.AllowBackgroundResponses.Value(); ok && v && thread == nil {
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
	if opts.ContinuationToken != nil {
		if len(messages) > 0 {
			return retError(errors.New("messages are not allowed when continuing a background response using a continuation token"))
		}
		if thread.ConversationID == "" && thread.MessageStore == nil {
			return retError(errors.New("continuation tokens are not allowed to be used for initial runs"))
		}
	}
	if opts.ContinuationToken == nil {
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
				if len(ctxData.Tools) > 0 {
					opts.Tools = append(opts.Tools, ctxData.Tools...)
				}
				if ctxData.Instructions != "" {
					if opts.Instructions != "" {
						opts.Instructions += "\n"
					}
					opts.Instructions += ctxData.Instructions
				}
			}
		}
		// Add the input messages to the end of thread messages.
		msgsForClient = append(msgsForClient, messages...)
	}
	// If a user provided two different thread ids, via the thread object and options, we should throw
	// since we don't know which one to use.
	if thread.ConversationID != "" && opts.ConversationID != "" && thread.ConversationID != opts.ConversationID {
		return retError(errors.New("conflicting conversation IDs provided in thread and chat options"))
	}
	if a.Instructions() != "" {
		if opts.Instructions != "" {
			opts.Instructions = "\n" + opts.Instructions
		}
		opts.Instructions = a.Instructions() + opts.Instructions
	}
	if thread.ConversationID != "" && opts.ConversationID != thread.ConversationID {
		opts.ConversationID = thread.ConversationID
	}
	return thread, opts, msgsForClient, ctxMessages, nil
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

func (a *Agent) createConfiguredChatOptions(options []agentopt.Option) ChatOptions {
	var opts ChatOptions
	// Try to get ChatOptions from RunOptions
	if v, ok := agentopt.Get(options, WithOptions); ok {
		opts = *v.Clone()
	}
	// Merge in Agent-level ChatOptions
	if a.Options.ChatOptions != nil {
		opts.Copy(a.Options.ChatOptions)
	}
	// Merge in RunOptions specific fields
	if v, ok := agentopt.Get(options, agentopt.AllowBackgroundResponses); ok {
		opts.AllowBackgroundResponses = param.NewOpt(v)
	}
	if v, ok := agentopt.Get(options, agentopt.ContinuationToken); ok {
		opts.ContinuationToken = v
	}
	if v, ok := agentopt.Get(options, agentopt.Stream); ok {
		opts.Stream = param.NewOpt(v)
	}
	if v, ok := agentopt.Get(options, agentopt.ResponseFormat); ok {
		opts.ResponseFormat = v
	}
	return opts
}
