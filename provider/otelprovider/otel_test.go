// Copyright (c) Microsoft. All rights reserved.

package otelprovider_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	otellib "go.opentelemetry.io/otel"
)

func setupTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	originalProvider := otellib.GetTracerProvider()
	otellib.SetTracerProvider(tp)

	t.Cleanup(func() {
		_ = tp.Shutdown(t.Context())
		otellib.SetTracerProvider(originalProvider)
	})
	return exporter
}

func TestOtel_Run_CreatesSpan(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	nextCalled := false
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		nextCalled = true
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
		}
	}

	messages := []*message.Message{message.NewText("test message")}

	seq := mw.Run(next, t.Context(), messages)
	for range seq {
	}

	if !nextCalled {
		t.Error("expected next function to be called")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
}

func TestOtel_Run_SpanHasCorrectAttributes(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	var capturedCtx context.Context

	// Override the agent metadata for this test
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				capturedCtx = ctx
				yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
			}
		},
	}, agent.Config{
		ID:          "test-agent-id",
		Name:        "test-agent",
		Description: "A test agent",
		Middlewares: []agent.Middleware{mw},
	})

	// Run through agent to get metadata in context
	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	_ = capturedCtx // silence unused warning

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}

	span := spans[len(spans)-1]

	// Check span name matches agent name
	if span.Name != "test-agent" {
		t.Errorf("expected span name 'test-agent', got %s", span.Name)
	}

	// Check attributes
	attrs := make(map[string]string)
	for _, attr := range span.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}

	expectedAttrs := map[string]string{
		"gen_ai.operation.name":    "invoke_agent",
		"gen_ai.provider.name":     "test-provider",
		"gen_ai.agent.id":          "test-agent-id",
		"gen_ai.agent.name":        "test-agent",
		"gen_ai.agent.description": "A test agent",
	}

	for key, expected := range expectedAttrs {
		if got, ok := attrs[key]; !ok {
			t.Errorf("expected attribute %q to be present", key)
		} else if got != expected {
			t.Errorf("expected attribute %q to be %q, got %q", key, expected, got)
		}
	}
}

func TestOtel_Run_RecordsError(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	testErr := errors.New("test error")
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, testErr)
		}
	}

	messages := []*message.Message{message.NewText("test message")}

	seq := mw.Run(next, t.Context(), messages)
	for range seq {
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	span := spans[0]

	// Check that error was recorded
	hasErrorEvent := false
	for _, event := range span.Events {
		if event.Name == "exception" {
			hasErrorEvent = true
			break
		}
	}

	if !hasErrorEvent {
		t.Error("expected span to have an exception event")
	}

	// Check span status is error
	if span.Status.Code != 1 { // codes.Error = 1 in OTel Go SDK
		t.Errorf("expected span status code to be Error (1), got %d", span.Status.Code)
	}

	if span.Status.Description != "test error" {
		t.Errorf("expected span status description to be 'test error', got %s", span.Status.Description)
	}
}

func TestOtel_Run_CustomSourceName(t *testing.T) {
	exporter := setupTracer(t)

	customSource := "my-custom-source"
	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{SourceName: customSource})

	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{}, nil)
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for range seq {
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	// The instrumentation scope name should be our custom source
	if spans[0].InstrumentationScope.Name != customSource {
		t.Errorf("expected instrumentation scope name %q, got %q", customSource, spans[0].InstrumentationScope.Name)
	}
}

func TestOtel_Run_DefaultSourceName(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{}, nil)
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for range seq {
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	expectedSource := "github.com/microsoft/agent-framework-go"
	if spans[0].InstrumentationScope.Name != expectedSource {
		t.Errorf("expected instrumentation scope name %q, got %q", expectedSource, spans[0].InstrumentationScope.Name)
	}
}

func TestOtel_Run_PropagatesContext(t *testing.T) {
	setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	var capturedCtx context.Context
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		capturedCtx = ctx
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{}, nil)
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for range seq {
	}

	if capturedCtx == nil {
		t.Fatal("context was not propagated")
	}

	// Check that the context has a span
	span := trace.SpanFromContext(capturedCtx)
	if span == nil {
		t.Error("expected context to have a span")
	}

	// The span should be recording
	if !span.SpanContext().IsValid() {
		t.Error("expected span context to be valid")
	}
}

func TestOtel_Run_HandlesMultipleUpdates(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	updateCount := 0
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for i := 0; i < 5; i++ {
				if !yield(&agent.ResponseUpdate{MessageID: "test"}, nil) {
					return
				}
			}
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for range seq {
		updateCount++
	}

	if updateCount != 5 {
		t.Errorf("expected 5 updates, got %d", updateCount)
	}

	// Should still have only one span
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

func TestOtel_Run_HandlesEarlyBreak(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for i := 0; i < 10; i++ {
				if !yield(&agent.ResponseUpdate{MessageID: "test"}, nil) {
					return
				}
			}
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})

	count := 0
	for range seq {
		count++
		if count >= 3 {
			break
		}
	}

	if count != 3 {
		t.Errorf("expected 3 updates before break, got %d", count)
	}

	// Span should still be ended
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

func TestOtel_Run_UnknownProviderWhenNoMetadata(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{}, nil)
		}
	}

	// Use context without metadata
	seq := mw.Run(next, t.Context(), []*message.Message{})
	for range seq {
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	attrs := make(map[string]string)
	for _, attr := range spans[0].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}

	// When no metadata, provider should be "unknown"
	if attrs["gen_ai.provider.name"] != "unknown" {
		t.Errorf("expected provider name to be 'unknown', got %s", attrs["gen_ai.provider.name"])
	}
}

func TestOtel_Run_RecordsMultipleErrors(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	err1 := errors.New("first error")
	err2 := errors.New("second error")

	callCount := 0
	next := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{}, err1) {
				return
			}
			yield(&agent.ResponseUpdate{}, err2)
		}
	}

	seq := mw.Run(next, t.Context(), []*message.Message{})
	for range seq {
		callCount++
	}

	if callCount != 2 {
		t.Errorf("expected 2 iterations, got %d", callCount)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	// Check both errors were recorded
	errorCount := 0
	for _, event := range spans[0].Events {
		if event.Name == "exception" {
			errorCount++
		}
	}

	if errorCount != 2 {
		t.Errorf("expected 2 exception events, got %d", errorCount)
	}
}
