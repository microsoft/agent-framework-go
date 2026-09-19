// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/internal/toolmiddleware"
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
//
// Middleware implementations must treat input message and option slices, and
// existing messages, as read-only. To modify the downstream invocation, clone
// the slices and any message being changed, then pass the derived values to next.
type Middleware interface {
	Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error]
}

// MiddlewareFunc adapts a function to the [Middleware] interface.
type MiddlewareFunc func(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error]

// Run calls the underlying function, letting a plain func be used wherever a
// [Middleware] is required.
func (mf MiddlewareFunc) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	return mf(next, ctx, messages, options...)
}

// FunctionInvocationContext describes a function invocation intercepted by middleware.
type FunctionInvocationContext struct {
	// Function is the underlying tool. Treat this field as read-only; changing it
	// does not change the tool invoked by the continuation.
	Function tool.FuncTool

	// CallID identifies the originating function call. Treat this field as read-only;
	// changing it does not change the ID used by the framework for its result.
	// It is empty when the provider supplied no ID or there is no invocation context.
	CallID string

	// Arguments contains the raw JSON passed to the tool. Middleware may replace
	// it before calling next. The tool retains responsibility for input validation.
	Arguments string
}

// FunctionInvocationFunc invokes the next function middleware or underlying tool.
type FunctionInvocationFunc func(context.Context, *FunctionInvocationContext) (any, error)

// FunctionInvocationMiddleware intercepts function calls by wrapping tools in
// agent options. Call next to continue execution, or return a result/error without
// calling next to replace the tool's behavior. Results and errors may also be
// inspected or replaced after next returns.
// Skipping next replaces only this invocation; it does not terminate the agent loop.
//
// Register it as a [Middleware] before the automatic tool-call middleware and
// after components that supply tools. For tools from context providers, use
// [ProviderConfig.Middlewares]. Function tools configured separately on the
// automatic tool-call middleware are also wrapped, without adding them to provider requests.
// Approval requirements and tool schemas are preserved. Callbacks run only when
// the tool is invoked, including after approval, not when approval is requested.
//
// Multiple callbacks execute in registration order, with the first outermost.
// Each call gets its own FunctionInvocationContext; callbacks must synchronize
// shared application state if tools can execute concurrently.
type FunctionInvocationMiddleware func(ctx context.Context, invocation *FunctionInvocationContext, next FunctionInvocationFunc) (any, error)

// Run wraps the function tools passed to next without modifying the input options.
func (mf FunctionInvocationMiddleware) Run(next RunFunc, ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
	if mf == nil {
		return next(ctx, messages, options...)
	}
	options = slices.Clone(options)
	for i, option := range options {
		opt, ok := option.(toolOpt)
		if !ok {
			continue
		}
		fn, ok := opt.Tool.(tool.FuncTool)
		if !ok {
			continue
		}
		options[i] = WithTool(mf.wrap(fn))
	}
	options = append(options, toolmiddleware.Wrapper(mf.wrap))
	return next(ctx, messages, options...)
}

func (mf FunctionInvocationMiddleware) wrap(fn tool.FuncTool) tool.FuncTool {
	wrapped := &functionInvocationTool{FuncTool: fn, middlewares: []FunctionInvocationMiddleware{mf}}
	if previous, ok := fn.(*functionInvocationTool); ok {
		wrapped.FuncTool = previous.FuncTool
		wrapped.middlewares = append(slices.Clone(previous.middlewares), mf)
	}
	return wrapped
}

type functionInvocationTool struct {
	tool.FuncTool
	middlewares []FunctionInvocationMiddleware
}

func (t *functionInvocationTool) ApprovalRequired() bool {
	approval, ok := t.FuncTool.(tool.ApprovalRequiredTool)
	return ok && approval.ApprovalRequired()
}

func (t *functionInvocationTool) Call(ctx context.Context, args string) (any, error) {
	identity, _ := tool.InvocationFromContext(ctx)
	invocation := &FunctionInvocationContext{Function: t.FuncTool, CallID: identity.CallID, Arguments: args}
	var next FunctionInvocationFunc = func(ctx context.Context, invocation *FunctionInvocationContext) (any, error) {
		return t.FuncTool.Call(ctx, invocation.Arguments)
	}
	for _, middleware := range slices.Backward(t.middlewares) {
		inner := next
		next = func(ctx context.Context, invocation *FunctionInvocationContext) (any, error) {
			return middleware(ctx, invocation, inner)
		}
	}
	return next(ctx, invocation)
}

// compileRunChain applies the given middlewares around fn.
func compileRunChain(fn RunFunc, middlewares []Middleware) RunFunc {
	for _, mw := range slices.Backward(middlewares) {
		if mw == nil {
			continue
		}
		fn = middlewareRunner{
			Middleware: mw,
			next:       fn,
		}.Run
	}
	return fn
}

type middlewareRunner struct {
	Middleware
	next RunFunc
}

func (mr middlewareRunner) Run(ctx context.Context, messages []*message.Message, opts ...Option) iter.Seq2[*ResponseUpdate, error] {
	next := func(ctx context.Context, outMessages []*message.Message, opts ...Option) iter.Seq2[*ResponseUpdate, error] {
		inputMessages := make(map[*message.Message]struct{}, len(messages))
		for _, msg := range messages {
			inputMessages[msg] = struct{}{}
		}
		var outMessagesCloned bool
		for i, msg := range outMessages {
			if _, ok := inputMessages[msg]; ok {
				continue
			}
			if msg == nil || msg.Source != (message.Source{}) {
				continue
			}
			if !outMessagesCloned {
				outMessages = slices.Clone(outMessages)
				outMessagesCloned = true
			}
			outMessages[i] = msg.WithSource(message.Source{Type: SourceTypeMiddleware})
		}
		return mr.next(ctx, outMessages, opts...)
	}
	return mr.Middleware.Run(next, ctx, messages, opts...)
}
