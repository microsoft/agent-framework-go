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
func Run(ctx context.Context, a Agent, opts ...agentopt.Option) (*RunResponse, error) {
	resp := RunResponse{
		AgentID: a.Identity().ID(),
	}
	for update, err := range a.Run(ctx, opts...) {
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

// RunStream executes the agent with the given options and returns a streaming sequence of response updates.
func RunStream(ctx context.Context, a Agent, opts ...agentopt.Option) iter.Seq2[*RunResponseUpdate, error] {
	opts = append(opts, agentopt.Stream(true))
	return a.Run(ctx, opts...)
}

// RunText executes the agent with a single text message and returns the response.
func RunText(ctx context.Context, a Agent, msg string, opts ...agentopt.Option) (*RunResponse, error) {
	return Run(ctx, a, append(opts, agentopt.Message(message.NewText(msg)))...)
}

// RunTextStream executes the agent with a single text message and returns a streaming sequence of response updates.
func RunTextStream(ctx context.Context, a Agent, msg string, opts ...agentopt.Option) iter.Seq2[*RunResponseUpdate, error] {
	return RunStream(ctx, a, append(opts, agentopt.Message(message.NewText(msg)))...)
}

// RunFor executes the agent with the given messages and returns the result of type T.
func RunFor[T any](ctx context.Context, a Agent, opts ...agentopt.Option) (T, *RunResponse, error) {
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
	resp, err := Run(ctx, a, opts...)
	if err != nil {
		return v, resp, err
	}
	err = formatter.Unmarshal([]byte(resp.String()), format, &v)
	return v, resp, err
}
