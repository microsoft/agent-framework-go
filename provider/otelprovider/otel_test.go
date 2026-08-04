// Copyright (c) Microsoft. All rights reserved.

package otelprovider_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"

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

	// Check span name is "<operation> <target>" per GenAI conventions
	if span.Name != "invoke_agent test-agent" {
		t.Errorf("expected span name 'invoke_agent test-agent', got %s", span.Name)
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

func TestOtel_Run_SpanNamePrefixedWithOperation(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	a := agent.New(agent.ProviderConfig{
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
			}
		},
	}, agent.Config{
		Name:        "helper",
		Middlewares: []agent.Middleware{mw},
	})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	// GenAI conventions require the span be named "<operation> <target>", matching the
	// sibling execute_tool span and the .NET/Python SDKs.
	if spans[0].Name != "invoke_agent helper" {
		t.Errorf("expected span name %q, got %q", "invoke_agent helper", spans[0].Name)
	}
}

func TestOtel_Run_SpanNameFallsBackToAgentID(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	// Agent with an id but no name: the span target falls back to the agent id,
	// matching .NET/Python.
	a := agent.New(agent.ProviderConfig{
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
			}
		},
	}, agent.Config{
		ID:          "agent-123",
		Middlewares: []agent.Middleware{mw},
	})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	// With no name, the target is the agent id.
	if spans[0].Name != "invoke_agent agent-123" {
		t.Errorf("expected span name %q, got %q", "invoke_agent agent-123", spans[0].Name)
	}
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "gen_ai.agent.id" && attr.Value.AsString() != "agent-123" {
			t.Errorf("expected gen_ai.agent.id %q, got %q", "agent-123", attr.Value.AsString())
		}
	}
}

func TestOtel_Run_OmitsEmptyAgentNameAndDescription(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	// Agent with no name and no description: .NET and Python omit these attributes
	// rather than emitting empty strings.
	a := agent.New(agent.ProviderConfig{
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "test-1"}, nil)
			}
		},
	}, agent.Config{
		Middlewares: []agent.Middleware{mw},
	})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	for _, attr := range spans[0].Attributes {
		if key := string(attr.Key); key == "gen_ai.agent.name" || key == "gen_ai.agent.description" {
			t.Errorf("expected %q to be omitted for an unnamed/undescribed agent", key)
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

	// Check error.type attribute is set (parity with Python capture_exception)
	attrs := make(map[string]string)
	for _, attr := range span.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["error.type"] != "errorString" {
		t.Errorf("expected error.type %q, got %q", "errorString", attrs["error.type"])
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

func TestOtel_Run_EmitsExecuteToolSpanForAutocall(t *testing.T) {
	exporter := setupTracer(t)

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call-1", Name: "get_weather", Arguments: `{}`},
				},
			}).
			NewTurn().
			AddText("sunny").
			Build(),
	}

	var toolSpanContext trace.SpanContext
	getWeather := functool.MustNew(
		functool.Config{Name: "get_weather", Description: "Returns the current weather"},
		func(ctx context.Context, args struct{}) (string, error) {
			toolSpanContext = trace.SpanContextFromContext(ctx)
			return "sunny", nil
		},
	)

	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		ID:   "test-agent-id",
		Name: "test-agent",
		Middlewares: []agent.Middleware{
			otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{SourceName: "test-source"}),
			toolautocall.New(toolautocall.Config{}),
		},
		Tools: []tool.Tool{getWeather},
	})

	_, err := a.RunMessage(t.Context(), message.NewText("weather?")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !toolSpanContext.IsValid() {
		t.Fatal("expected tool call to observe a valid span context")
	}

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	invokeSpan := findSpanByOperation(t, spans, "invoke_agent")
	executeToolSpan := findSpanByOperation(t, spans, "execute_tool")

	if executeToolSpan.InstrumentationScope.Name != "test-source" {
		t.Fatalf("expected execute_tool span scope %q, got %q", "test-source", executeToolSpan.InstrumentationScope.Name)
	}
	if executeToolSpan.Parent.SpanID() != invokeSpan.SpanContext.SpanID() {
		t.Fatalf("expected execute_tool parent %s, got %s", invokeSpan.SpanContext.SpanID(), executeToolSpan.Parent.SpanID())
	}
	if executeToolSpan.Name != "execute_tool get_weather" {
		t.Fatalf("expected execute_tool span name %q, got %q", "execute_tool get_weather", executeToolSpan.Name)
	}

	executeToolAttrs := make(map[string]string)
	for _, attr := range executeToolSpan.Attributes {
		executeToolAttrs[string(attr.Key)] = attr.Value.AsString()
	}
	if executeToolAttrs["gen_ai.tool.call.id"] != "call-1" {
		t.Errorf("expected gen_ai.tool.call.id %q, got %q", "call-1", executeToolAttrs["gen_ai.tool.call.id"])
	}
	if executeToolAttrs["gen_ai.tool.type"] != "function" {
		t.Errorf("expected gen_ai.tool.type %q, got %q", "function", executeToolAttrs["gen_ai.tool.type"])
	}
	if executeToolAttrs["gen_ai.tool.description"] != "Returns the current weather" {
		t.Errorf("expected gen_ai.tool.description %q, got %q", "Returns the current weather", executeToolAttrs["gen_ai.tool.description"])
	}
}

func TestOtel_Run_ExecuteToolSpan_EmptyCallIDFallsBackToUnknown(t *testing.T) {
	exporter := setupTracer(t)

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					// No CallID set — simulates an LLM that omits the call ID field.
					&message.FunctionCallContent{Name: "get_weather", Arguments: `{}`},
				},
			}).
			NewTurn().
			AddText("sunny").
			Build(),
	}

	getWeather := functool.MustNew(
		functool.Config{Name: "get_weather"},
		func(ctx context.Context, args struct{}) (string, error) {
			return "sunny", nil
		},
	)

	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		Name: "test-agent",
		Middlewares: []agent.Middleware{
			otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{SourceName: "test-source"}),
			toolautocall.New(toolautocall.Config{}),
		},
		Tools: []tool.Tool{getWeather},
	})

	_, err := a.RunMessage(t.Context(), message.NewText("weather?")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	executeToolSpan := findSpanByOperation(t, spans, "execute_tool")

	attrs := make(map[string]string)
	for _, attr := range executeToolSpan.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["gen_ai.tool.call.id"] != "unknown" {
		t.Errorf("expected gen_ai.tool.call.id %q when CallID is empty, got %q", "unknown", attrs["gen_ai.tool.call.id"])
	}
	if _, hasDesc := attrs["gen_ai.tool.description"]; hasDesc {
		t.Errorf("expected gen_ai.tool.description to be absent when tool has no description")
	}
}

func TestOtel_Run_ExecuteToolSpan_SetsErrorTypeOnToolError(t *testing.T) {
	exporter := setupTracer(t)

	toolErr := errors.New("tool failure")

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{CallID: "call-err", Name: "failing_tool", Arguments: `{}`},
				},
			}).
			NewTurn().
			AddText("done").
			Build(),
	}

	failingTool := functool.MustNew(
		functool.Config{Name: "failing_tool"},
		func(ctx context.Context, args struct{}) (string, error) {
			return "", toolErr
		},
	)

	a := agent.New(agent.ProviderConfig{
		Run: runner.Run,
	}, agent.Config{
		Name: "test-agent",
		Middlewares: []agent.Middleware{
			otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{SourceName: "test-source"}),
			toolautocall.New(toolautocall.Config{}),
		},
		Tools: []tool.Tool{failingTool},
	})

	_, err := a.RunMessage(t.Context(), message.NewText("go")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()
	executeToolSpan := findSpanByOperation(t, spans, "execute_tool")

	attrs := make(map[string]string)
	for _, attr := range executeToolSpan.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["error.type"] != "errorString" {
		t.Errorf("expected error.type %q, got %q", "errorString", attrs["error.type"])
	}

	hasErrorEvent := false
	for _, event := range executeToolSpan.Events {
		if event.Name == "exception" {
			hasErrorEvent = true
			break
		}
	}
	if !hasErrorEvent {
		t.Error("expected execute_tool span to have an exception event")
	}
}

func findSpanByOperation(t *testing.T, spans []tracetest.SpanStub, operation string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		for _, attr := range span.Attributes {
			if string(attr.Key) == "gen_ai.operation.name" && attr.Value.AsString() == operation {
				return span
			}
		}
	}
	t.Fatalf("expected span with operation %q", operation)
	return tracetest.SpanStub{}
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

func TestOtel_Run_RecordsTokenUsage(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})

	// Two updates, each carrying usage -- an agent that makes several LLM round-trips
	// emits one UsageContent per round-trip. The span must report the SUM, not the last
	// one, or a multi-turn agent under-reports what it actually spent.
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{
					MessageID: "turn-1",
					Contents: []message.Content{&message.UsageContent{
						Details: message.UsageDetails{
							InputTokenCount:       100,
							OutputTokenCount:      10,
							TotalTokenCount:       110,
							CachedInputTokenCount: 5,
						},
					}},
				}, nil)
				yield(&agent.ResponseUpdate{
					MessageID: "turn-2",
					Contents: []message.Content{&message.UsageContent{
						Details: message.UsageDetails{
							InputTokenCount:  200,
							OutputTokenCount: 20,
							TotalTokenCount:  220,
						},
					}},
				}, nil)
			}
		},
	}, agent.Config{
		ID:          "test-agent-id",
		Name:        "test-agent",
		Middlewares: []agent.Middleware{mw},
	})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}
	attrs := map[string]any{}
	for _, attr := range spans[len(spans)-1].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}

	for key, want := range map[string]int64{
		"gen_ai.usage.input_tokens":            300, // 100 + 200
		"gen_ai.usage.output_tokens":           30,  // 10 + 20
		"gen_ai.usage.cache_read.input_tokens": 5,
	} {
		got, ok := attrs[key]
		if !ok {
			t.Fatalf("span is missing %s", key)
		}
		if got != want {
			t.Fatalf("%s = %v, want %d", key, got, want)
		}
	}

	// A provider that reports no reasoning tokens must not get a zero-valued attribute:
	// "absent" and "zero" mean different things, and an always-present always-zero
	// attribute trains people to ignore it.
	if _, ok := attrs["gen_ai.usage.reasoning.output_tokens"]; ok {
		t.Fatal("reasoning.output_tokens should be omitted when the provider reports none")
	}
}

func TestOtel_Run_NoUsageAttributesWhenProviderReportsNone(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{MessageID: "no-usage"}, nil)
			}
		},
	}, agent.Config{ID: "id", Name: "n", Middlewares: []agent.Middleware{mw}})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}
	for _, attr := range spans[len(spans)-1].Attributes {
		if strings.HasPrefix(string(attr.Key), "gen_ai.usage.") {
			t.Fatalf("unexpected usage attribute %s on a run with no usage reported", attr.Key)
		}
	}
}

// Regression for the guard bug caught in review: setUsage originally returned early when
// input/output/total were all zero, which silently dropped the ENTIRE attribute set for a
// provider that reported only cached (or only reasoning) tokens. "Did we see any usage?"
// has to consider every counter, not just the three required ones.
func TestOtel_Run_RecordsUsageWhenOnlyOptionalCountersReported(t *testing.T) {
	exporter := setupTracer(t)

	mw := otelprovider.NewMiddleware(otelprovider.MiddlewareConfig{})
	a := agent.New(agent.ProviderConfig{
		ProviderName: "test-provider",
		Run: func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{
					MessageID: "cached-only",
					Contents: []message.Content{&message.UsageContent{
						// Only a cached count. input/output/total all zero.
						Details: message.UsageDetails{CachedInputTokenCount: 42},
					}},
				}, nil)
			}
		},
	}, agent.Config{ID: "id", Name: "n", Middlewares: []agent.Middleware{mw}})

	_, _ = a.RunMessage(t.Context(), message.NewText("test")).Collect()

	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least 1 span")
	}
	attrs := map[string]any{}
	for _, attr := range spans[len(spans)-1].Attributes {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}

	got, ok := attrs["gen_ai.usage.cache_read.input_tokens"]
	if !ok {
		t.Fatal("cache_read.input_tokens was dropped when it was the only counter reported")
	}
	if got != int64(42) {
		t.Fatalf("cache_read.input_tokens = %v, want 42", got)
	}
}
