// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
)

type ChatOptions = chatclient.ChatOptions

var _ agent.Agent = (*Agent)(nil)

type Agent struct {
	Client  chatclient.Client
	Options Options

	id string
}

// NewAgent creates a new chat agent with the given chat client and options.
func NewAgent(client chatclient.Client, options *Options) *Agent {
	var opts Options
	if options != nil {
		opts = *options.Clone()
		options = nil // prevent further use of the original options
	}
	if !opts.UseProvidedChatClientAsIs {
		client = chatclient.NewFunctionInvoking(client, &chatclient.FunctionInvokingOptions{
			Logger: opts.Logger,
		})
	}
	return &Agent{
		Client:  client,
		Options: opts,
	}
}

func (a *Agent) ID() string {
	if a.Options.ID != "" {
		return a.Options.ID
	}
	if a.id == "" {
		a.id = uuid.NewString()
	}
	return a.id
}

func (a *Agent) Name() string {
	return a.Options.Name
}

func (a *Agent) Description() string {
	return a.Options.Description
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

// RunOf executes the agent with the given value type and messages, and stores the result
// in the value pointed to by v.
func (a *Agent) RunOf(v any, ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
	return a.run(ctx, func(ctx context.Context, client chatclient.Client, opts *chatclient.ChatOptions, messages []*message.Message) (*chatclient.ChatResponse, error) {
		c, ok := client.(chatclient.StructuredResponseClient)
		if !ok {
			return nil, fmt.Errorf("the %T chat client doesn't support structured responses", client)
		}
		return c.StructuredResponse(v, ctx, opts, messages...)
	}, messages)
}

// RunFor executes the agent with the given messages and returns the result of type T.
func RunFor[T any](a *Agent, ctx *agent.RunContext, messages ...*message.Message) (T, *agent.RunResponse, error) {
	var v T
	resp, err := a.RunOf(&v, ctx, messages...)
	return v, resp, err
}

// RunText executes the agent with a single text message and returns the response.
func (a *Agent) RunText(ctx *agent.RunContext, msg string) (*agent.RunResponse, error) {
	return a.Run(ctx, message.NewText(msg))
}

// Run executes the agent with the given messages and returns the response.
func (a *Agent) Run(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
	return a.run(ctx, func(ctx context.Context, client chatclient.Client, opts *chatclient.ChatOptions, messages []*message.Message) (*chatclient.ChatResponse, error) {
		return client.Response(ctx, opts, messages...)
	}, messages)
}

func (a *Agent) run(actx *agent.RunContext,
	runFn func(context.Context, chatclient.Client, *chatclient.ChatOptions, []*message.Message) (*chatclient.ChatResponse, error),
	messages []*message.Message) (*agent.RunResponse, error) {

	ctx, thread, opts, messages, ctxMessages, err := a.prepareThreadAndMessages(actx, messages)
	if err != nil {
		return nil, err
	}
	client := applyRunOptionsTransformations(actx.GetOptions(), a.Client)
	resp, err := runFn(ctx, client, opts, messages)
	if err != nil {
		a.notifyContextProviderOfFailure(ctx, thread, err, messages, ctxMessages)
		return nil, err
	}

	// Ensure that the author name is set for each message in the response.
	for _, msg := range resp.Messages {
		msg.AuthorName = cmp.Or(msg.AuthorName, a.Name())
	}
	if err := a.finishRun(ctx, thread, resp, messages, ctxMessages); err != nil {
		return nil, err
	}
	return &agent.RunResponse{
		AgentID:   a.ID(),
		ID:        resp.ID,
		CreatedAt: resp.CreatedAt,
		Messages:  resp.Messages,
		Usage:     resp.Usage,
	}, nil
}

// RunStream executes the agent with the given messages and returns a streaming response.
func (a *Agent) RunStream(actx *agent.RunContext, messages ...*message.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	client := applyRunOptionsTransformations(actx.GetOptions(), a.Client)
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		ctx, thread, opts, messages, ctxMessages, err := a.prepareThreadAndMessages(actx, messages)
		if err != nil {
			yield(nil, err)
			return
		}
		var updates []*chatclient.ChatResponseUpdate
		for update, err := range client.StreamingResponse(ctx, opts, messages...) {
			if err != nil {
				a.notifyContextProviderOfFailure(ctx, thread, err, messages, ctxMessages)
				yield(nil, err)
				return
			}
			if update != nil {
				update.AuthorName = cmp.Or(update.AuthorName, a.Name())
				updates = append(updates, update)
				if !yield(&agent.RunResponseUpdate{
					AgentID:    a.ID(),
					AuthorName: update.AuthorName,
					MessageID:  update.MessageID,
					ResponseID: update.ResponseID,
					CreatedAt:  update.CreatedAt,
					Role:       update.Role,
					Contents:   update.Contents,
				}, nil) {
					return
				}
			}
		}
		resp := chatclient.NewChatResponseFromUpdates(updates)
		if err := a.finishRun(ctx, thread, resp, messages, ctxMessages); err != nil {
			yield(nil, err)
			return
		}
	}
}

func (a *Agent) finishRun(ctx context.Context, thread *Thread, resp *chatclient.ChatResponse, messages, ctxMessages []*message.Message) error {
	// We can derive the type of supported thread from whether we have a conversation id,
	// so let's update it and set the conversation id for the service thread case.
	if err := a.updateThreadWithTypeAndConversationID(thread, resp.ConversationID); err != nil {
		return err
	}
	// Only notify the thread of new messages if the chatResponse was successful to avoid inconsistent message state in the thread.
	if err := thread.MessagesReceived(ctx, append(ctxMessages, resp.Messages...)...); err != nil {
		return err
	}
	// Notify the ContextProvider of all new messages.
	return a.notifyContextProviderOfSuccess(ctx, thread, messages, ctxMessages, resp.Messages)
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

func (a *Agent) prepareThreadAndMessages(ctx *agent.RunContext, messages []*message.Message) (_ context.Context, thread *Thread, opts *ChatOptions, msgsForClient, ctxMessages []*message.Message, err error) {
	retError := func(e error) (context.Context, *Thread, *ChatOptions, []*message.Message, []*message.Message, error) {
		return nil, nil, nil, nil, nil, e
	}
	opts = a.createConfiguredChatOptions(ctx.GetOptions())
	if opts != nil {
		if v, ok := opts.AllowBackgroundResponses.Value(); ok && v && thread == nil {
			return retError(errors.New("a thread must be provided when continuing a background response with a continuation token"))
		}
	}
	if cthread := ctx.GetThread(); cthread != nil {
		var ok bool
		thread, ok = cthread.(*Thread)
		if !ok {
			return retError(errors.New("the provided thread is not compatible with the agent, only threads created by the agent can be used"))
		}
	} else {
		thread = a.newThread("")
	}
	if opts != nil && opts.ContinuationToken != nil {
		if len(messages) > 0 {
			return retError(errors.New("messages are not allowed when continuing a background response using a continuation token"))
		}
	}

	if opts == nil || opts.ContinuationToken == nil {
		if thread.MessageStore != nil {
			//  Add any existing messages from the thread to the messages to be sent to the chat client.
			for msg, err := range thread.MessageStore.All(ctx.GetContext()) {
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
				Context:  ctx.GetContext(),
				Messages: messages,
			})
			if err != nil {
				return retError(err)
			}
			if ctxData != nil {
				ctxMessages = ctxData.Messages
				msgsForClient = append(msgsForClient, ctxData.Messages...)
				if len(ctxData.Tools) > 0 {
					if opts == nil {
						opts = &ChatOptions{}
					}
					opts.Tools = append(opts.Tools, ctxData.Tools...)
				}
				if ctxData.Instructions != "" {
					if opts == nil {
						opts = &ChatOptions{}
					}
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
	if thread.ConversationID != "" && opts != nil && opts.ConversationID != "" && thread.ConversationID != opts.ConversationID {
		return retError(errors.New("conflicting conversation IDs provided in thread and chat options"))
	}
	if a.Instructions() != "" {
		if opts == nil {
			opts = &ChatOptions{}
		}
		if opts.Instructions != "" {
			opts.Instructions = "\n" + opts.Instructions
		}
		opts.Instructions = a.Instructions() + opts.Instructions
	}
	if thread.ConversationID != "" && opts != nil && opts.ConversationID != thread.ConversationID {
		// Only create or update ChatOptions if we have an id on the thread and we don't have the same one already in ChatOptions.
		if opts == nil {
			opts = &ChatOptions{}
		}
		opts.ConversationID = thread.ConversationID
	}
	return ctx.GetContext(), thread, opts, msgsForClient, ctxMessages, nil
}

func (a *Agent) createConfiguredChatOptions(runOpts *agent.RunOptions) *ChatOptions {
	var opts *ChatOptions
	// Try to get ChatOptions from RunOptions
	if runOpts != nil && runOpts.Options != nil {
		if v, ok := runOpts.Options.(*ChatOptions); ok {
			opts = v.Clone()
		}
	}
	// Merge in Agent-level ChatOptions
	if a.Options.ChatOptions != nil {
		if opts == nil {
			opts = a.Options.ChatOptions.Clone()
		} else {
			opts.Copy(a.Options.ChatOptions)
		}
	}
	// Merge in RunOptions specific fields
	if runOpts != nil && (runOpts.AllowBackgroundResponses.Valid() || runOpts.ContinuationToken != nil) {
		if opts == nil {
			opts = &ChatOptions{}
		}
		opts.AllowBackgroundResponses = runOpts.AllowBackgroundResponses
		opts.ContinuationToken = runOpts.ContinuationToken
	}
	return opts
}

func applyRunOptionsTransformations(opts *agent.RunOptions, client chatclient.Client) chatclient.Client {
	if opts == nil {
		return client
	}
	if v, ok := opts.Options.(*RunOptions); ok && v.NewClient != nil {
		// If we have a custom chat client factory, we should use it to create a new chat client with the transformed tools.
		client = v.NewClient(client)
	}
	return client
}
