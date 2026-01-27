// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

type withMiddlewareOpt struct{ Middleware }

func (o withMiddlewareOpt) Value() any {
	return o.Middleware
}

func (withMiddlewareOpt) RunOption() {}

func With(mw Middleware) agentopt.RunOption {
	return withMiddlewareOpt{mw}
}

func WithFunc(fn Func) agentopt.RunOption {
	return withMiddlewareOpt{fn}
}

type RunFunc = func(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]

type Middleware interface {
	Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]
}

type Func func(next RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error]

func (mf Func) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return mf(next, ctx, messages, options...)
}

// RunChain applies the given middlewares around the given RunFunc.
func RunChain(ctx context.Context, fn RunFunc, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	// Chain the middlewares together.
	for mw := range agentopt.AllBackward(options, With) {
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

func (mr middlewareRunner) Run(ctx context.Context, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return mr.Middleware.Run(mr.next, ctx, messages, opts...)
}
