// Copyright (c) Microsoft. All rights reserved.

package workflow

import "context"

// TraceContextPropagator extracts and injects distributed trace context for
// cross-executor propagation within a workflow. Implementations typically
// delegate to a concrete tracing library (e.g. OpenTelemetry).
//
// Extract serialises the active span context from ctx into a map suitable for
// wire propagation. It returns nil when there is nothing to propagate.
//
// Inject deserialises previously extracted trace context from a map back into
// a new context derived from ctx.
type TraceContextPropagator interface {
	Extract(ctx context.Context) map[string]string
	Inject(ctx context.Context, tc map[string]string) context.Context
}
