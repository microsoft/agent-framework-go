// Copyright (c) Microsoft. All rights reserved.

package opentelemetry

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/microsoft/agent-framework-go/workflow/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const defaultSourceName = "Microsoft.Agents.AI.Workflows"

// Config holds configuration for workflow OpenTelemetry instrumentation.
type Config struct {
	// SourceName is the OpenTelemetry instrumentation scope name.
	// If empty, Microsoft.Agents.AI.Workflows is used.
	SourceName string
}

// New creates a workflow tracer backed by OpenTelemetry.
func New(config Config) observability.Tracer {
	return otelTracer{tracer: otel.Tracer(cmp.Or(config.SourceName, defaultSourceName))}
}

type otelTracer struct {
	tracer trace.Tracer
}

func (t otelTracer) Start(ctx context.Context, name string, options observability.SpanOptions) (context.Context, observability.Span) {
	startOptions := make([]trace.SpanStartOption, 0, 2)
	if options.Kind == observability.SpanKindProducer {
		startOptions = append(startOptions, trace.WithSpanKind(trace.SpanKindProducer))
	}
	if link, ok := sourceLink(ctx, options.SourceTraceContext); ok {
		startOptions = append(startOptions, trace.WithLinks(link))
	}
	ctx, span := t.tracer.Start(ctx, name, startOptions...)
	return ctx, otelSpan{span: span}
}

func (t otelTracer) ExtractTraceContext(ctx context.Context) map[string]string {
	carrier := make(mapCarrier)
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	return map[string]string(carrier)
}

type otelSpan struct {
	span trace.Span
}

func (s otelSpan) End() {
	s.span.End()
}

func (s otelSpan) AddEvent(name string, attrs ...observability.Attribute) {
	s.span.AddEvent(name, trace.WithAttributes(otelAttributes(attrs)...))
}

func (s otelSpan) SetAttributes(attrs ...observability.Attribute) {
	s.span.SetAttributes(otelAttributes(attrs)...)
}

func (s otelSpan) RecordError(err error) {
	s.span.RecordError(err, trace.WithTimestamp(time.Now()))
}

func (s otelSpan) SetError(message string) {
	s.span.SetStatus(codes.Error, message)
}

func otelAttributes(attrs []observability.Attribute) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	result := make([]attribute.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		result = append(result, otelAttribute(attr))
	}
	return result
}

func otelAttribute(attr observability.Attribute) attribute.KeyValue {
	switch value := attr.Value.(type) {
	case string:
		return attribute.String(attr.Key, value)
	case bool:
		return attribute.Bool(attr.Key, value)
	case int:
		return attribute.Int(attr.Key, value)
	case int64:
		return attribute.Int64(attr.Key, value)
	case float64:
		return attribute.Float64(attr.Key, value)
	default:
		return attribute.String(attr.Key, fmt.Sprint(value))
	}
}

func sourceLink(ctx context.Context, traceContext map[string]string) (trace.Link, bool) {
	if len(traceContext) == 0 {
		return trace.Link{}, false
	}
	injected := otel.GetTextMapPropagator().Extract(ctx, mapCarrier(traceContext))
	spanContext := trace.SpanContextFromContext(injected)
	if !spanContext.IsValid() {
		return trace.Link{}, false
	}
	return trace.Link{SpanContext: spanContext}, true
}

type mapCarrier map[string]string

func (c mapCarrier) Get(key string) string { return c[key] }
func (c mapCarrier) Set(key, value string) { c[key] = value }
func (c mapCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
