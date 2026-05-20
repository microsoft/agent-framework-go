// Copyright (c) Microsoft. All rights reserved.

package messageworkflow

import (
	"iter"
	"reflect"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

type Options struct {
	// Required fields
	StateKey        string
	TakeTurnHandler func(ctx *workflow.Context, token workflow.TurnToken, messages []*message.Message) error

	// Optional fields
	StringMessageRole        string
	ScopeName                string
	DisableAutoSendTurnToken bool
	MessageState             *MessageState
}

type MessageState struct {
	cache workflow.StatefulExecutorCache[[]*message.Message]
}

func NewMessageState(stateKey string, scopeName string) *MessageState {
	return &MessageState{
		cache: workflow.StatefulExecutorCache[[]*message.Message]{
			StateKey:  stateKey,
			ScopeName: scopeName,
		},
	}
}

func (s *MessageState) ProcessTurnMessages(ctx *workflow.Context, fn func(ctx *workflow.Context, messages []*message.Message) ([]*message.Message, error)) error {
	if fn == nil {
		panic("messageworkflow: process function is required")
	}
	return s.cache.InvokeWithState(ctx, false, fn)
}

func (s *MessageState) Reset() error {
	return s.cache.Reset()
}

// Configure extends spec with chat-message workflow behavior.
func Configure(spec *workflow.ExecutorSpec, options *Options) {
	if spec == nil {
		panic("messageworkflow: spec is required")
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
	messageSpec := workflow.ExecutorSpec{
		DisableAutoSendMessageHandlerResultObject: true,
		DisableAutoYieldOutputHandlerResultObject: true,
		Reset: state.Reset,
		OnCheckpointRestored: func(ctx *workflow.Context) error {
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
	spec.Extend(messageSpec)
}
