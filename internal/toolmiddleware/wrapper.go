// Copyright (c) Microsoft. All rights reserved.

// Package toolmiddleware shares tool wrappers and invocation metadata between
// agent middleware and tool execution.
package toolmiddleware

import (
	"context"

	"github.com/microsoft/agent-framework-go/tool"
)

// Wrapper is an internal run option applied by tool execution to each function
// tool, independently of how the tool was registered.
type Wrapper func(tool.FuncTool) tool.FuncTool

// MAFValue implements agent.Option without depending on the agent package.
func (w Wrapper) MAFValue() any { return w }

type invocationKey struct{}

type invocationMetadata struct {
	callID    string
	terminate *bool
}

// WithCallID associates the current tool invocation's call ID with ctx.
// It clears any termination control inherited from another invocation.
func WithCallID(ctx context.Context, callID string) context.Context {
	return WithInvocation(ctx, callID, nil)
}

// WithInvocation associates callID and per-call termination control with ctx.
func WithInvocation(ctx context.Context, callID string, terminate *bool) context.Context {
	return context.WithValue(ctx, invocationKey{}, invocationMetadata{callID: callID, terminate: terminate})
}

// CallIDFromContext returns the call ID for the current tool invocation.
func CallIDFromContext(ctx context.Context) (string, bool) {
	invocation, ok := ctx.Value(invocationKey{}).(invocationMetadata)
	return invocation.callID, ok
}

// RequestTermination requests termination if the current executor supports it.
func RequestTermination(ctx context.Context) bool {
	invocation, _ := ctx.Value(invocationKey{}).(invocationMetadata)
	if invocation.terminate == nil {
		return false
	}
	*invocation.terminate = true
	return true
}
