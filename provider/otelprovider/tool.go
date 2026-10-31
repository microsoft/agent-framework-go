// Copyright (c) Microsoft. All rights reserved.

package otelprovider

import (
	"cmp"
	"context"
	"time"

	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// NewToolTracer creates a tracer for [toolautocall.Config.Tracer] or
// [toolautocall.WithTracer] using the global OpenTelemetry tracer provider.
// An empty sourceName uses
// "github.com/microsoft/agent-framework-go". The caller owns the provider's lifetime.
// Tool arguments and results are not recorded.
//
// [NewMiddleware] already supplies automatic tool tracing. Use NewToolTracer to
// trace tools independently of agent middleware or to override their source name.
func NewToolTracer(sourceName string) toolautocall.Tracer {
	return toolTracer{tracer: otel.Tracer(cmp.Or(sourceName, defaultSourceName))}
}

type toolTracer struct {
	tracer trace.Tracer
}

func (t toolTracer) StartToolCall(ctx context.Context, call *message.FunctionCallContent, definition tool.Tool) (context.Context, func(error)) {
	const operation = "execute_tool"
	name := operation
	if call.Name != "" {
		name += " " + call.Name
	}
	attrs := []attribute.KeyValue{
		semconv.GenAIOperationNameKey.String(operation),
		semconv.GenAIToolName(call.Name),
		semconv.GenAIToolCallID(cmp.Or(call.CallID, "unknown")),
		semconv.GenAIToolType("function"),
	}
	if desc := definition.Description(); desc != "" {
		attrs = append(attrs, semconv.GenAIToolDescription(desc))
	}
	ctx, span := t.tracer.Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, func(err error) {
		defer span.End()
		if err != nil {
			span.SetAttributes(semconv.ErrorTypeKey.String(telemetry.ErrorTypeName(err)))
			span.RecordError(err, trace.WithTimestamp(time.Now()))
			span.SetStatus(codes.Error, err.Error())
		}
	}
}
