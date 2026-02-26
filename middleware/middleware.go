// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

type RunFunc = func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error]

type Middleware interface {
	Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error]
}

type Func func(next RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error]

func (mf Func) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return mf(next, ctx, messages, options...)
}

// RunChain applies the given middlewares around the given RunFunc.
func RunChain(ctx context.Context, fn RunFunc, middlewares []Middleware, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	// Chain the middlewares together.
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw := middlewares[i]
		fn = middlewareRunner{
			Middleware: mw,
			next:       fn,
		}.Run
	}
	return fn(ctx, messages, options...)
}

type middlewareRunner struct {
	Middleware
	next RunFunc
}

func (mr middlewareRunner) Run(ctx context.Context, messages []*message.Message, opts ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return mr.Middleware.Run(mr.next, ctx, messages, opts...)
}
