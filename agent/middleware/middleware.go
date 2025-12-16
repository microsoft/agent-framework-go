// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/message"
)

type RunFunc func(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error]

type Middleware interface {
	Run(ctx context.Context, next RunFunc, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error]
}

// RunChain applies the given middlewares around the given RunFunc.
func RunChain(ctx context.Context, fn RunFunc, middlewares []Middleware, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	// Chain the middlewares together.
	for _, mw := range slices.Backward(middlewares) {
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
	return mr.Middleware.Run(ctx, mr.next, messages, opts...)
}
