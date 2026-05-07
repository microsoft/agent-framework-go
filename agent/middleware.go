// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/message"
)

// Middleware wraps an agent run function to inspect or modify messages, options,
// response updates, and errors.
//
// Use middleware when an extension needs direct control over provider invocation,
// streaming updates, option propagation, or error handling beyond the
// request/response message hooks exposed by [ContextProvider].
type Middleware interface {
	Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error]
}

// MiddlewareFunc adapts a function to the [Middleware] interface.
type MiddlewareFunc func(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error]

func (mf MiddlewareFunc) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return mf(next, ctx, messages, options...)
}

// runChain applies the given middlewares around the given RunFunc.
func runChain(ctx context.Context, fn RunFunc, middlewares []Middleware, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
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

func (mr middlewareRunner) Run(ctx context.Context, messages []*message.Message, opts ...Option) iter.Seq2[*ResponseUpdate, error] {
	return mr.Middleware.Run(mr.next, ctx, messages, opts...)
}
