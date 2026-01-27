// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

type identityKey struct{}

type identity struct {
	id   string
	name string
}

func IdentityFromContext(ctx context.Context) (id, name string, ok bool) {
	if v := ctx.Value(identityKey{}); v != nil {
		ident := v.(identity)
		return ident.id, ident.name, true
	}
	return "", "", false
}

// Run executes the agent with the given options and returns the response.
func Run(ctx context.Context, a Agent, messages []*message.Message, opts ...agentopt.RunOption) (*message.Response, error) {
	var resp message.Response
	for update, err := range a.Run(ctx, messages, opts...) {
		if err != nil {
			return nil, err
		}
		resp.Update(update)
	}
	resp.Coalesce()
	return &resp, nil
}

// RunText executes the agent with a single text message and returns the response.
func RunText(ctx context.Context, a Agent, msg string, opts ...agentopt.RunOption) (*message.Response, error) {
	return Run(ctx, a, []*message.Message{message.NewText(msg)}, opts...)
}

// RunMessage executes the agent with a single message and returns the response.
func RunMessage(ctx context.Context, a Agent, msg *message.Message, opts ...agentopt.RunOption) (*message.Response, error) {
	return Run(ctx, a, []*message.Message{msg}, opts...)
}

// RunStream executes the agent with the given options and returns a streaming sequence of response updates.
func RunStream(ctx context.Context, a Agent, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	opts = append(opts, agentopt.Stream(true))
	return a.Run(ctx, messages, opts...)
}

// RunTextStream executes the agent with a single text message and returns a streaming sequence of response updates.
func RunTextStream(ctx context.Context, a Agent, msg string, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return RunStream(ctx, a, []*message.Message{message.NewText(msg)}, opts...)
}

// RunMessageStream executes the agent with a single message and returns a streaming sequence of response updates.
func RunMessageStream(ctx context.Context, a Agent, msg *message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return RunStream(ctx, a, []*message.Message{msg}, opts...)
}

// RunTextFor executes the agent with a single text message and returns the result of type T.
func RunTextFor[T any](ctx context.Context, a Agent, msg string, opts ...agentopt.RunOption) (T, error) {
	return RunFor[T](ctx, a, []*message.Message{message.NewText(msg)}, opts...)
}

// RunMessageFor executes the agent with a single message and returns the result of type T.
func RunMessageFor[T any](ctx context.Context, a Agent, msg *message.Message, opts ...agentopt.RunOption) (T, error) {
	return RunFor[T](ctx, a, []*message.Message{msg}, opts...)
}

// RunFor executes the agent with the given messages and returns the result of type T.
func RunFor[T any](ctx context.Context, a Agent, messages []*message.Message, opts ...agentopt.RunOption) (T, error) {
	var v T
	opts = append(opts, agentopt.StructuredOutput(&v))
	_, err := Run(ctx, a, messages, opts...)
	return v, err
}
