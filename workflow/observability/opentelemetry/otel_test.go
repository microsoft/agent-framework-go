// Copyright (c) Microsoft. All rights reserved.

package opentelemetry_test

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow/observability"
	workflowotel "github.com/microsoft/agent-framework-go/workflow/observability/opentelemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func setupTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	originalProvider := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(originalProvider)
		otel.SetTextMapPropagator(originalPropagator)
	})
	return exporter
}

func TestNewCreatesWorkflowTracer(t *testing.T) {
	exporter := setupTracer(t)

	tracer := workflowotel.New(workflowotel.Config{SourceName: "test-source"})
	ctx, span := tracer.Start(context.Background(), "workflow.build", observability.SpanOptions{})
	span.SetAttributes(observability.Attribute{Key: "workflow.id", Value: "start"})
	span.AddEvent("build.started")
	span.End()

	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name != "workflow.build" {
		t.Fatalf("span name = %q, want workflow.build", spans[0].Name)
	}
	if spans[0].InstrumentationScope.Name != "test-source" {
		t.Fatalf("scope name = %q, want test-source", spans[0].InstrumentationScope.Name)
	}
	if got := spans[0].Attributes[0].Value.AsString(); got != "start" {
		t.Fatalf("workflow.id = %q, want start", got)
	}
	if len(spans[0].Events) != 1 || spans[0].Events[0].Name != "build.started" {
		t.Fatalf("expected build.started event, got %#v", spans[0].Events)
	}
}

func TestNewUsesDefaultWorkflowSourceName(t *testing.T) {
	exporter := setupTracer(t)

	tracer := workflowotel.New(workflowotel.Config{})
	_, span := tracer.Start(context.Background(), "workflow.build", observability.SpanOptions{})
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].InstrumentationScope.Name; got != "Microsoft.Agents.AI.Workflows" {
		t.Fatalf("scope name = %q, want Microsoft.Agents.AI.Workflows", got)
	}
}

func TestTracerLinksSourceTraceContext(t *testing.T) {
	exporter := setupTracer(t)

	parentTracer := otel.Tracer("parent")
	parentCtx, parentSpan := parentTracer.Start(context.Background(), "parent")
	traceContext := workflowotel.New(workflowotel.Config{}).ExtractTraceContext(parentCtx)
	parentSpan.End()

	tracer := workflowotel.New(workflowotel.Config{})
	_, span := tracer.Start(context.Background(), "executor.process start", observability.SpanOptions{SourceTraceContext: traceContext})
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	parentIndex, childIndex := -1, -1
	for i, span := range spans {
		switch span.Name {
		case "parent":
			parentIndex = i
		case "executor.process start":
			childIndex = i
		}
	}
	if parentIndex < 0 || childIndex < 0 {
		t.Fatalf("expected parent and child spans, got %#v", spans)
	}
	parent := spans[parentIndex]
	child := spans[childIndex]
	if len(child.Links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(child.Links))
	}
	if child.Links[0].SpanContext.TraceID() != parent.SpanContext.TraceID() {
		t.Fatalf("link trace ID = %s, want %s", child.Links[0].SpanContext.TraceID(), parent.SpanContext.TraceID())
	}
}
