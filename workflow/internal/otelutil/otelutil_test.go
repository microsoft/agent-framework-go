// Copyright (c) Microsoft. All rights reserved.

package otelutil

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestExtractInjectRoundTrip(t *testing.T) {
	// Set up a real tracer and W3C propagator.
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	// Extract the trace context from the context with an active span.
	tc := ExtractTraceContext(ctx)
	if tc == nil {
		t.Fatal("expected non-nil trace context from active span")
	}
	if tc["traceparent"] == "" {
		t.Fatal("expected traceparent header in trace context")
	}

	// Inject the trace context into a fresh context.
	restoredCtx := InjectTraceContext(context.Background(), tc)
	restoredSpanCtx := trace.SpanContextFromContext(restoredCtx)

	if !restoredSpanCtx.IsValid() {
		t.Fatal("expected valid span context after injection")
	}
	if restoredSpanCtx.TraceID() != span.SpanContext().TraceID() {
		t.Errorf("trace ID mismatch: got %s, want %s", restoredSpanCtx.TraceID(), span.SpanContext().TraceID())
	}
}

func TestExtractTraceContext_NoSpan(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tc := ExtractTraceContext(context.Background())
	if tc != nil {
		t.Errorf("expected nil trace context from background context, got %v", tc)
	}
}

func TestInjectTraceContext_NilMap(t *testing.T) {
	ctx := InjectTraceContext(context.Background(), nil)
	if ctx != context.Background() {
		t.Error("expected same context when injecting nil trace context")
	}
}
