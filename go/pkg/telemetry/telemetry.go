// Copyright (c) Microsoft. All rights reserved.

package telemetry

import (
	"context"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "github.com/microsoft/agent-framework/go"
)

// Tracer provides OpenTelemetry tracing for agent operations.
type Tracer struct {
	tracer trace.Tracer
	meter  metric.Meter
}

// NewTracer creates a new Tracer.
func NewTracer() *Tracer {
	return &Tracer{
		tracer: otel.Tracer(instrumentationName),
		meter:  otel.Meter(instrumentationName),
	}
}

// StartAgentRun creates a span for an agent run.
func (t *Tracer) StartAgentRun(ctx context.Context, agentID, agentName string) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, "agent.run",
		trace.WithAttributes(
			attribute.String("agent.id", agentID),
			attribute.String("agent.name", agentName),
		),
	)
}

// StartToolCall creates a span for a tool execution.
func (t *Tracer) StartToolCall(ctx context.Context, toolName, toolID string) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, "tool.call",
		trace.WithAttributes(
			attribute.String("tool.name", toolName),
			attribute.String("tool.id", toolID),
		),
	)
}

// StartChatCompletion creates a span for a chat completion.
func (t *Tracer) StartChatCompletion(ctx context.Context, modelID string, messageCount int) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, "chat.completion",
		trace.WithAttributes(
			attribute.String("model.id", modelID),
			attribute.Int("message.count", messageCount),
		),
	)
}

// RecordUsage records token usage metrics.
func (t *Tracer) RecordUsage(ctx context.Context, modelID string, usage *agent.UsageDetails) {
	if usage == nil {
		return
	}

	// Record as span events
	span := trace.SpanFromContext(ctx)
	span.AddEvent("usage",
		trace.WithAttributes(
			attribute.String("model.id", modelID),
			attribute.Int64("tokens.input", usage.InputTokenCount),
			attribute.Int64("tokens.output", usage.OutputTokenCount),
			attribute.Int64("tokens.total", usage.TotalTokenCount),
		),
	)

	// TODO: Record as metrics
}

// RecordError records an error event.
func (t *Tracer) RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}

// SetSpanAttributes adds attributes to the current span.
func (t *Tracer) SetSpanAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}
