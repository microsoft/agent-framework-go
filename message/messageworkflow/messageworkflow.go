// Copyright (c) Microsoft. All rights reserved.

// Package messageworkflow extends a workflow executor with chat-message
// handling behavior, accumulating the messages received during a turn and
// invoking a caller-supplied handler when a turn token arrives.
package messageworkflow

import (
	"iter"
	"reflect"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

// Options configures the chat-message workflow behavior applied by Configure.
// StateKey and TakeTurnHandler are required; Configure panics if StateKey is
// empty or TakeTurnHandler is nil. The remaining fields are optional.
type Options struct {
	// StateKey identifies the accumulated turn state within the workflow.
	StateKey string
	// TakeTurnHandler is invoked with the accumulated messages when a turn token arrives.
	TakeTurnHandler func(ctx *workflow.Context, token workflow.TurnToken, messages []*message.Message) error

	// StringMessageRole, when set, registers a handler that wraps incoming string messages with this role.
	StringMessageRole string
	// ScopeName scopes the accumulated turn state; used when constructing a default MessageState.
	ScopeName string
	// DisableAutoSendTurnToken suppresses automatically declaring and forwarding the turn token.
	DisableAutoSendTurnToken bool
	// MessageState supplies an existing accumulator; when nil a new one is created from StateKey and ScopeName.
	MessageState *MessageState
}

// MessageState is the checkpoint-restorable accumulator for a turn's messages.
// It wraps a StatefulExecutorCache holding the slice of messages received so far.
type MessageState struct {
	cache workflow.StatefulExecutorCache[[]*message.Message]
}

// NewMessageState returns a MessageState whose accumulated messages are keyed
// by stateKey within the given scopeName.
func NewMessageState(stateKey string, scopeName string) *MessageState {
	return &MessageState{
		cache: workflow.StatefulExecutorCache[[]*message.Message]{
			StateKey:  stateKey,
			ScopeName: scopeName,
		},
	}
}

// ProcessTurnMessages transforms the accumulated turn messages by invoking fn
// via InvokeWithState, which reads the current state, calls fn, and queues the
// returned slice as the new accumulated state.
func (s *MessageState) ProcessTurnMessages(ctx *workflow.Context, fn func(ctx *workflow.Context, messages []*message.Message) ([]*message.Message, error)) error {
	if fn == nil {
		panic("messageworkflow: process function is required")
	}
	return s.cache.InvokeWithState(ctx, false, fn)
}

// Reset clears the accumulated turn state. It is registered both as the
// executor's reset function and as its checkpoint-restored callback.
func (s *MessageState) Reset() error {
	return s.cache.Reset()
}

// Configure extends executor with chat-message workflow behavior.
func Configure(executor *workflow.Executor, options *Options) {
	if executor == nil {
		panic("messageworkflow: executor is required")
	}
	if options.StateKey == "" {
		panic("stateKey is required")
	}
	if options.TakeTurnHandler == nil {
		panic("TakeTurnHandler is required")
	}
	state := options.MessageState
	if state == nil {
		state = NewMessageState(options.StateKey, options.ScopeName)
	}
	messageExecutor := workflow.Executor{
		ResetFunc: state.Reset,
		OnCheckpointRestoredFunc: func(ctx *workflow.Context) error {
			return state.Reset()
		},
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			if !options.DisableAutoSendTurnToken {
				rb.SendsMessageType(reflect.TypeFor[workflow.TurnToken]())
			}
			if options.StringMessageRole != "" {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, state.ProcessTurnMessages(ctx, func(ctx *workflow.Context, messages []*message.Message) ([]*message.Message, error) {
						return append(messages, &message.Message{
							Role:     message.Role(options.StringMessageRole),
							Contents: []message.Content{&message.TextContent{Text: msg.(string)}},
						}), nil
					})
				})
			}
			rb.RouteBuilder.
				AddHandlerRaw(reflect.TypeFor[*message.Message](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, state.ProcessTurnMessages(ctx, func(ctx *workflow.Context, messages []*message.Message) ([]*message.Message, error) {
						return append(messages, msg.(*message.Message)), nil
					})
				}).
				AddHandlerRaw(reflect.TypeFor[[]*message.Message](), nil, func(ctx *workflow.Context, msgs any) (any, error) {
					return struct{}{}, state.ProcessTurnMessages(ctx, func(ctx *workflow.Context, messages []*message.Message) ([]*message.Message, error) {
						return append(messages, msgs.([]*message.Message)...), nil
					})
				}).
				AddHandlerRaw(reflect.TypeFor[iter.Seq[*message.Message]](), nil, func(ctx *workflow.Context, msgs any) (any, error) {
					return struct{}{}, state.ProcessTurnMessages(ctx, func(ctx *workflow.Context, messages []*message.Message) ([]*message.Message, error) {
						for msg := range msgs.(iter.Seq[*message.Message]) {
							messages = append(messages, msg)
						}
						return messages, nil
					})
				}).
				AddHandlerRaw(reflect.TypeFor[workflow.TurnToken](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, state.ProcessTurnMessages(ctx, func(ctx *workflow.Context, messages []*message.Message) ([]*message.Message, error) {
						token := msg.(workflow.TurnToken)
						if err := options.TakeTurnHandler(ctx, token, messages); err != nil {
							return nil, err
						}
						if !options.DisableAutoSendTurnToken {
							if err := ctx.SendMessage("", token); err != nil {
								return nil, err
							}
						}
						return nil, nil
					})
				})
			return rb, nil
		},
	}
	executor.Extend(&messageExecutor)
}
