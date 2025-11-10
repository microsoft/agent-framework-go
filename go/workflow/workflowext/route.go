// Copyright (c) Microsoft. All rights reserved.

package workflowext

import (
	"context"
	"reflect"

	"github.com/microsoft/agent-framework/go/workflow"
)

func RouteBuilderAddHandlerVoid[In any](rb *workflow.RouteBuilder, overwrite bool, handler func(context.Context, workflow.Context, In)) {
	rb.AddHandler(reflect.TypeFor[In](), nil, overwrite, func(ctx context.Context, wctx workflow.Context, msg any) workflow.CallResult {
		handler(ctx, wctx, msg.(In))
		return workflow.CallResult{IsVoid: true}
	})
}

func RouteBuilderAddHandler1[In any](rb *workflow.RouteBuilder, overwrite bool, handler func(context.Context, workflow.Context, In) error) {
	rb.AddHandler(reflect.TypeFor[In](), nil, overwrite, func(ctx context.Context, wctx workflow.Context, msg any) workflow.CallResult {
		err := handler(ctx, wctx, msg.(In))
		return workflow.CallResult{Error: err}
	})
}

func RouteBuilderAddHandler2[In, Out any](rb *workflow.RouteBuilder, overwrite bool, handler func(context.Context, workflow.Context, In) (Out, error)) {
	rb.AddHandler(reflect.TypeFor[In](), reflect.TypeFor[Out](), overwrite, func(ctx context.Context, wctx workflow.Context, msg any) workflow.CallResult {
		result, err := handler(ctx, wctx, msg.(In))
		return workflow.CallResult{Result: result, Error: err}
	})
}

func RouteBuilderAddCatchAllVoid(rb *workflow.RouteBuilder, overwrite bool, handler func(context.Context, workflow.Context, workflow.Value)) {
	rb.AddCatchAll(overwrite, func(ctx context.Context, wctx workflow.Context, msg workflow.Value) workflow.CallResult {
		handler(ctx, wctx, msg)
		return workflow.CallResult{IsVoid: true}
	})
}

func RouteBuilderAddCatchAll1(rb *workflow.RouteBuilder, overwrite bool, handler func(context.Context, workflow.Context, workflow.Value) error) {
	rb.AddCatchAll(overwrite, func(ctx context.Context, wctx workflow.Context, msg workflow.Value) workflow.CallResult {
		err := handler(ctx, wctx, msg)
		return workflow.CallResult{Error: err}
	})
}

func RouteBuilderAddCatchAll2[Out any](rb *workflow.RouteBuilder, overwrite bool, handler func(context.Context, workflow.Context, workflow.Value) (Out, error)) {
	rb.AddCatchAll(overwrite, func(ctx context.Context, wctx workflow.Context, msg workflow.Value) workflow.CallResult {
		result, err := handler(ctx, wctx, msg)
		return workflow.CallResult{Result: result, Error: err}
	})
}
