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

	// Arguments contains raw JSON, matching [tool.FuncTool.Call]. Middleware
	// receives it before any validation or normalization performed by the
	// underlying tool.
	//
	// Middleware may replace it before calling next. To inspect or change
	// individual fields, decode the JSON and encode any changes back into Arguments.
	Arguments string
}

// FunctionInvocationMiddleware intercepts individual function calls.
// Call next to continue execution, or return a result/error without
// calling next to replace the tool's behavior. Results and errors may also be
// inspected or replaced after next returns.
// Skipping next replaces only this invocation; it does not terminate the agent loop.
//
// Register it in [Config.FunctionMiddlewares], independently of agent middleware.
// Callbacks apply when tools are executed, including tools supplied by context
// providers and additional tools configured on automatic tool execution.
// Approval requirements and tool schemas are preserved. Callbacks run only when
// the tool is invoked, including after approval, not when approval is requested.
//
// Multiple callbacks execute in registration order, with the first outermost.
// Each call gets its own FunctionInvocationContext; callbacks must synchronize
// shared application state if tools can execute concurrently.
//
// Providers that execute tools themselves set [ProviderConfig.ManagesToolExecution]
// and use [WithFuncCallID] to identify each invocation.
type FunctionInvocationMiddleware func(next func(context.Context, *FunctionInvocationContext) (any, error), ctx context.Context, invocation *FunctionInvocationContext) (any, error)

// WithFuncCallID returns a context carrying callID for
// [FunctionInvocationContext.CallID]. Providers that execute tools themselves
// should pass the returned context to [tool.FuncTool.Call] so function invocation
// middleware receives the ID. Use an empty callID when the provider supplies no
// ID; this overrides any ID inherited from ctx.
func WithFuncCallID(ctx context.Context, callID string) context.Context {
	return toolmiddleware.WithCallID(ctx, callID)
}

// wrapFuncTools wraps option-provided function tools immediately before run,
// leaving the options seen by outer middleware unchanged.
func wrapFuncTools(run RunFunc) RunFunc {
	return func(ctx context.Context, messages []*message.Message, options ...Option) iter.Seq2[*ResponseUpdate, error] {
		var cloned bool
		for _, opt := range options {
			wrap, ok := opt.(toolmiddleware.Wrapper)
			if !ok {
				continue
			}
			for i, opt := range options {
				t, ok := opt.(toolOpt)
				if !ok {
					continue
				}
				fn, ok := t.Tool.(tool.FuncTool)
				if !ok {
					continue
				}
				if !cloned {
					options = slices.Clone(options)
					cloned = true
				}
				options[i] = WithTool(wrap(fn))
			}
		}
		return run(ctx, messages, options...)
	}
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
	callID, _ := toolmiddleware.CallIDFromContext(ctx)
	invocation := &FunctionInvocationContext{Function: t.FuncTool, CallID: callID, Arguments: args}
	next := func(ctx context.Context, invocation *FunctionInvocationContext) (any, error) {
		return t.FuncTool.Call(ctx, invocation.Arguments)
	}
	for _, middleware := range slices.Backward(t.middlewares) {
		inner := next
		next = func(ctx context.Context, invocation *FunctionInvocationContext) (any, error) {
			return middleware(inner, ctx, invocation)
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
