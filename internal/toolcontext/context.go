// Copyright (c) Microsoft. All rights reserved.

// Package toolcontext carries tool invocation metadata across framework packages.
package toolcontext

import "context"

type callIDKey struct{}

// WithCallID associates the current tool invocation's call ID with ctx.
func WithCallID(ctx context.Context, callID string) context.Context {
	return context.WithValue(ctx, callIDKey{}, callID)
}

// CallIDFromContext returns the call ID for the current tool invocation.
func CallIDFromContext(ctx context.Context) (string, bool) {
	callID, ok := ctx.Value(callIDKey{}).(string)
	return callID, ok
}
