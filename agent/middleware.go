// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

// SourceTypeMiddleware represents a message that originated from a middleware component.
const SourceTypeMiddleware message.SourceType = "middleware"

// Middleware wraps an agent run function to inspect or modify messages, options,
// response updates, and errors.
//
// Use middleware when an extension needs direct control over provider invocation,
// streaming updates, option propagation, or error handling beyond the
// request/response message hooks exposed by [ContextProvider].
// Messages passed to next that were not present in the middleware input are
// marked with [SourceTypeMiddleware] when they do not already carry a source.
type Middleware interface {
	Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error]
}

// MiddlewareFunc adapts a function to the [Middleware] interface.
type MiddlewareFunc func(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error]

func (mf MiddlewareFunc) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return mf(next, ctx, messages, options...)
}

// FunctionInvocationContext carries the state of a single tool (function)
// invocation as it flows through a chain of [FunctionInvocationMiddleware]
// executed by the automatic tool-calling loop (see the toolautocall harness).
//
// Unlike the run-level [Middleware], which only wraps a whole run, this hook
// intercepts each individual tool call. It mirrors the per-call interception
// offered by .NET's FunctionInvocationDelegatingAgent and Python's
// FunctionMiddleware.
type FunctionInvocationContext struct {
	// Function is the tool that will be invoked for this call.
	Function tool.Tool

	// Arguments holds the JSON-encoded arguments passed to the tool. A middleware
	// may replace this before calling next to change what the tool receives.
	Arguments string

	// CallContent is the originating function call content from the provider.
	CallContent *message.FunctionCallContent

	// Result holds the value returned by next (the inner middleware, or the tool
	// itself). It is populated before next returns so an outer middleware can
	// inspect the produced result.
	Result any

	// Terminate, when set to true by any middleware, stops the tool-calling loop
	// after the current round of function results has been returned to the caller.
	Terminate bool

	// Iteration is the zero-based tool-calling round in which this invocation runs.
	Iteration int
}

// FunctionInvocationMiddleware intercepts a single tool invocation performed by
// the automatic tool-calling loop. Implementations call next to proceed to the
// next middleware and ultimately the tool, or skip it to short-circuit the call.
// The value returned becomes the function result recorded for the provider.
//
// Middleware compose outermost-first: the first element of the configured slice
// runs first and wraps the rest, matching [runChain].
type FunctionInvocationMiddleware func(ctx context.Context, fic *FunctionInvocationContext, next func(context.Context) (any, error)) (any, error)

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
	next := func(ctx context.Context, outMessages []*message.Message, opts ...Option) iter.Seq2[*ResponseUpdate, error] {
		originals := make(map[*message.Message]struct{}, len(messages))
		for _, msg := range messages {
			originals[msg] = struct{}{}
		}
		for i, msg := range outMessages {
			if _, ok := originals[msg]; ok {
				continue
			}
			if msg == nil || msg.Source != (message.Source{}) {
				continue
			}
			marked := msg.Clone()
			marked.Source = message.Source{Type: SourceTypeMiddleware}
			outMessages[i] = marked
		}
		return mr.next(ctx, outMessages, opts...)
	}
	return mr.Middleware.Run(next, ctx, messages, opts...)
}
