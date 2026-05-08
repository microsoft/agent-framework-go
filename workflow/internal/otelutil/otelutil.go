// Copyright (c) Microsoft. All rights reserved.

package otelutil

import (
	"context"

	"github.com/microsoft/agent-framework-go/workflow"
	"go.opentelemetry.io/otel"
)

// mapCarrier implements propagation.TextMapCarrier over a map[string]string.
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

// Propagator is a [workflow.TraceContextPropagator] backed by the global
// OpenTelemetry text-map propagator.
var Propagator workflow.TraceContextPropagator = otelPropagator{}

type otelPropagator struct{}

// Extract extracts the current span context from ctx into a map[string]string
// suitable for wire propagation (e.g. W3C traceparent).
// Returns nil if injection produces no fields, for example when there is no
// valid active span context or the configured propagator emits nothing.
func (otelPropagator) Extract(ctx context.Context) map[string]string {
	prop := otel.GetTextMapPropagator()
	carrier := make(mapCarrier)
	prop.Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	return map[string]string(carrier)
}

// Inject restores span context from a map[string]string into a new context
// derived from ctx.
func (otelPropagator) Inject(ctx context.Context, tc map[string]string) context.Context {
	if len(tc) == 0 {
		return ctx
	}
	prop := otel.GetTextMapPropagator()
	return prop.Extract(ctx, mapCarrier(tc))
}
