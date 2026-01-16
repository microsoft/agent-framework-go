// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

type RunFunc func(ctx context.Context, a agent.Agent, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]

type Middleware interface {
	Run(next RunFunc, ctx context.Context, a agent.Agent, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]
}

type Func func(next RunFunc, ctx context.Context, a agent.Agent, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]

func (mf Func) Run(next RunFunc, ctx context.Context, a agent.Agent, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return mf(next, ctx, a, messages, options...)
}

// RunChain applies the given middlewares around the given RunFunc.
func RunChain(ctx context.Context, fn RunFunc, middlewares []Middleware, a agent.Agent, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	// Chain the middlewares together.
	for _, mw := range slices.Backward(middlewares) {
		fn = middlewareRunner{
			Middleware: mw,
			next:       fn,
		}.Run
	}
	return fn(ctx, a, messages, options...)
}

type middlewareRunner struct {
	Middleware
	next RunFunc
}

func (mr middlewareRunner) Run(ctx context.Context, a agent.Agent, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return mr.Middleware.Run(mr.next, ctx, a, messages, opts...)
}
