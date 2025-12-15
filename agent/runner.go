// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"errors"
	"iter"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

// Run executes the agent with the given options and returns the response.
func Run(ctx context.Context, a Agent, messages []*message.Message, opts ...agentopt.Option) (*RunResponse, error) {
	resp := RunResponse{
		AgentID: a.Identity().ID(),
	}
	for update, err := range a.Run(ctx, messages, opts...) {
		if err != nil {
			return nil, err
		}
		processUpdate(&resp, update)
	}
	for _, msg := range resp.Messages {
		msg.Contents = message.CoalesceContents(msg.Contents)
	}
	return &resp, nil
}

// RunText executes the agent with a single text message and returns the response.
func RunText(ctx context.Context, a Agent, msg string, opts ...agentopt.Option) (*RunResponse, error) {
	return Run(ctx, a, []*message.Message{message.NewText(msg)}, opts...)
}

// RunMessage executes the agent with a single message and returns the response.
func RunMessage(ctx context.Context, a Agent, msg *message.Message, opts ...agentopt.Option) (*RunResponse, error) {
	return Run(ctx, a, []*message.Message{msg}, opts...)
}

// RunStream executes the agent with the given options and returns a streaming sequence of response updates.
func RunStream(ctx context.Context, a Agent, messages []*message.Message, opts ...agentopt.Option) iter.Seq2[*RunResponseUpdate, error] {
	opts = append(opts, agentopt.Stream(true))
	return a.Run(ctx, messages, opts...)
}

// RunTextStream executes the agent with a single text message and returns a streaming sequence of response updates.
func RunTextStream(ctx context.Context, a Agent, msg string, opts ...agentopt.Option) iter.Seq2[*RunResponseUpdate, error] {
	return RunStream(ctx, a, []*message.Message{message.NewText(msg)}, opts...)
}

// RunMessageStream executes the agent with a single message and returns a streaming sequence of response updates.
func RunMessageStream(ctx context.Context, a Agent, msg *message.Message, opts ...agentopt.Option) iter.Seq2[*RunResponseUpdate, error] {
	return RunStream(ctx, a, []*message.Message{msg}, opts...)
}

// RunTextFor executes the agent with a single text message and returns the result of type T and the response.
func RunTextFor[T any](ctx context.Context, a Agent, msg string, opts ...agentopt.Option) (T, *RunResponse, error) {
	return RunFor[T](ctx, a, []*message.Message{message.NewText(msg)}, opts...)
}

// RunMessageFor executes the agent with a single message and returns the result of type T and the response.
func RunMessageFor[T any](ctx context.Context, a Agent, msg *message.Message, opts ...agentopt.Option) (T, *RunResponse, error) {
	return RunFor[T](ctx, a, []*message.Message{msg}, opts...)
}

// RunFor executes the agent with the given messages and returns the result of type T.
func RunFor[T any](ctx context.Context, a Agent, messages []*message.Message, opts ...agentopt.Option) (T, *RunResponse, error) {
	var v T
	formatter := a.Capabilities().StructuredOutput
	if formatter == nil {
		return v, nil, errors.New("agent does not support structured output")
	}
	format, err := formatter.Format(v)
	if err != nil {
		return v, nil, err
	}
	opts = append(opts, agentopt.ResponseFormat(format))
	resp, err := Run(ctx, a, messages, opts...)
	if err != nil {
		return v, resp, err
	}
	err = formatter.Unmarshal([]byte(resp.String()), format, &v)
	return v, resp, err
}
