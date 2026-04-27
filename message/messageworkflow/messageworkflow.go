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
	StringMessageRole string
	ScopeName         string
}

func NewExecutorConfig(options *Options) *workflow.ExecutorConfig {
	if options.StateKey == "" {
		panic("stateKey is required")
	}
	if options.TakeTurnHandler == nil {
		panic("TakeTurnHandler is required")
	}
	cache := &workflow.StatefulExecutorCache[[]*message.Message]{
		StateKey: options.StateKey,
		InitialStateFactory: func() []*message.Message {
			return nil
		},
		ScopeName: options.ScopeName,
	}
	return &workflow.ExecutorConfig{
		Reset: cache.Reset,
		ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
			if options.StringMessageRole != "" {
				rb.AddHandler(reflect.TypeFor[string](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, cache.InvokeWithState(ctx, false, func(ctx *workflow.Context, state []*message.Message) ([]*message.Message, error) {
						return append(state, &message.Message{
							Role:     message.Role(options.StringMessageRole),
							Contents: []message.Content{&message.TextContent{Text: msg.(string)}},
						},
						), nil
					})
				})
			}
			if options.TakeTurnHandler != nil {
				rb.AddHandler(reflect.TypeFor[workflow.TurnToken](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, cache.InvokeWithState(ctx, false, func(ctx *workflow.Context, state []*message.Message) ([]*message.Message, error) {
						token := msg.(workflow.TurnToken)
						if err := options.TakeTurnHandler(ctx, token, state); err != nil {
							return nil, err
						}
						if err := ctx.SendMessage("", token); err != nil {
							return nil, err
						}
						// Reset the state to empty list.
						return nil, nil
					})
				})
			}
			return rb.
				AddHandler(reflect.TypeFor[*message.Message](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
					return struct{}{}, cache.InvokeWithState(ctx, false, func(ctx *workflow.Context, state []*message.Message) ([]*message.Message, error) {
						return append(state, msg.(*message.Message)), nil
					})
				}).
				AddHandler(reflect.TypeFor[[]*message.Message](), nil, false, func(ctx *workflow.Context, msgs any) (any, error) {
					return struct{}{}, cache.InvokeWithState(ctx, false, func(ctx *workflow.Context, state []*message.Message) ([]*message.Message, error) {
						return append(state, msgs.([]*message.Message)...), nil
					})
				}).
				AddHandler(reflect.TypeFor[iter.Seq[*message.Message]](), nil, false, func(ctx *workflow.Context, msgs any) (any, error) {
					return struct{}{}, cache.InvokeWithState(ctx, false, func(ctx *workflow.Context, state []*message.Message) ([]*message.Message, error) {
						for msg := range msgs.(iter.Seq[*message.Message]) {
							state = append(state, msg)
						}
						return state, nil
					})
				}), nil
		},
	}
}
