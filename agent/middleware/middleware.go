// Copyright (c) Microsoft. All rights reserved.

package middleware

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
)

type RunFunc func(ctx context.Context, options ...agentopt.Option) iter.Seq2[*agent.RunResponseUpdate, error]

type Middleware interface {
	Run(ctx context.Context, next RunFunc, options ...agentopt.Option) iter.Seq2[*agent.RunResponseUpdate, error]
}

// RunChain applies the given middlewares around the given RunFunc.
func RunChain(ctx context.Context, fn RunFunc, middlewares []Middleware, opts []agentopt.Option) iter.Seq2[*agent.RunResponseUpdate, error] {
	// Chain the middlewares together.
	for _, mw := range slices.Backward(middlewares) {
		fn = middlewareRunner{
			Middleware: mw,
			next:       fn,
		}.Run
	}
	return fn(ctx, opts...)
}

type middlewareRunner struct {
	Middleware
	next RunFunc
}

func (mr middlewareRunner) Run(ctx context.Context, opts ...agentopt.Option) iter.Seq2[*agent.RunResponseUpdate, error] {
	return mr.Middleware.Run(ctx, mr.next, opts...)
}
