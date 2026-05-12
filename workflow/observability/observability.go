// Copyright (c) Microsoft. All rights reserved.

package observability

import "context"

// Attribute is a key-value pair recorded on workflow telemetry spans or events.
type Attribute struct {
	Key   string
	Value any
}

// StringAttribute creates a string telemetry attribute.
func StringAttribute(key, value string) Attribute {
	return Attribute{Key: key, Value: value}
}

// BoolAttribute creates a bool telemetry attribute.
func BoolAttribute(key string, value bool) Attribute {
	return Attribute{Key: key, Value: value}
}

// SpanKind describes the role of a workflow telemetry span.
type SpanKind int

const (
	// SpanKindInternal identifies internal workflow processing spans.
	SpanKindInternal SpanKind = iota

	// SpanKindProducer identifies spans that produce messages for later processing.
	SpanKindProducer
)

// SpanOptions configures a telemetry span start operation.
type SpanOptions struct {
	Kind               SpanKind
	SourceTraceContext map[string]string
}

// Tracer starts workflow telemetry spans and extracts trace context.
type Tracer interface {
	Start(ctx context.Context, name string, options SpanOptions) (context.Context, Span)
	ExtractTraceContext(ctx context.Context) map[string]string
}

// Span records workflow telemetry span data.
type Span interface {
	End()
	AddEvent(name string, attrs ...Attribute)
	SetAttributes(attrs ...Attribute)
	RecordError(err error)
	SetError(message string)
}
