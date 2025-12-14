// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

// Run executes the agent with the given options and returns the response.
func Run(ctx context.Context, a Agent, opts ...agentopt.Option) (*RunResponse, error) {
	resp := RunResponse{
		AgentID: a.Identity().ID(),
	}
	for update, err := range run(ctx, a, opts) {
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
	return run(ctx, a, opts)
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

// agentRunner is a Runner that runs an Agent, validating and populating updates as needed.
type agentRunner struct {
	agent Agent
}

func (ar agentRunner) Run(ctx context.Context, opts ...agentopt.Option) iter.Seq2[*RunResponseUpdate, error] {
	return func(yield func(*RunResponseUpdate, error) bool) {
		iden := ar.agent.Identity()
		id, name := iden.ID(), iden.Name()
		for update, err := range ar.agent.Run(ctx, opts...) {
			if update == nil && err == nil {
				if !yield(nil, fmt.Errorf("agent %s (%s) returned nil update", id, name)) {
					return
				}
				continue
			}
			if update != nil {
				if update.AgentID == "" {
					update.AgentID = id
				}
				if update.AuthorName == "" {
					update.AuthorName = name
				}
			}
			if !yield(update, err) {
				return
			}
		}
	}
}

type middlewareRunner struct {
	Middleware
	next Runner
}

func (mr middlewareRunner) Run(ctx context.Context, opts ...agentopt.Option) iter.Seq2[*RunResponseUpdate, error] {
	return mr.Middleware.Run(ctx, mr.next, opts...)
}

func run(ctx context.Context, a Agent, opts []agentopt.Option) iter.Seq2[*RunResponseUpdate, error] {
	// If no thread is provided, create a new one.
	if _, ok := agentopt.Get(opts, agentopt.Thread); !ok {
		opts = append(opts, agentopt.Thread(a.NewThread()))
	}

	caps := a.Capabilities()

	// Collect tools from agent capabilities.
	for _, t := range caps.Tools {
		opts = append(opts, agentopt.Tool(t))
	}

	// Collect all middlewares and add the agent's Run method as the final middleware.
	middlewares := slices.Clone(caps.Middlewares)

	// Chain the middlewares together.
	var chain Runner = agentRunner{agent: a}
	for _, mw := range slices.Backward(middlewares) {
		chain = middlewareRunner{
			Middleware: mw,
			next:       chain,
		}
	}
	return chain.Run(ctx, opts...)
}
