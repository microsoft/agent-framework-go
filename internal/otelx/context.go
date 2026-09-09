// Copyright (c) Microsoft. All rights reserved.

package otelx

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

type tracerContextKey struct{}

// WithTracer stores tracer on the context for nested middleware/tool spans.
func WithTracer(ctx context.Context, tracer trace.Tracer) context.Context {
	if tracer == nil {
		return ctx
	}
	return context.WithValue(ctx, tracerContextKey{}, tracer)
}

// TracerFromContext returns the tracer stored with [WithTracer].
func TracerFromContext(ctx context.Context) (trace.Tracer, bool) {
	tracer, ok := ctx.Value(tracerContextKey{}).(trace.Tracer)
	return tracer, ok && tracer != nil
}
