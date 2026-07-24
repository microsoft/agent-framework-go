// Copyright (c) Microsoft. All rights reserved.

package observability_test

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow/internal/observability"
	workflowobservability "github.com/microsoft/agent-framework-go/workflow/observability"
)

type fakeSpan struct {
	attrs map[string]any
}

func (s *fakeSpan) End() {}

func (s *fakeSpan) AddEvent(name string, attrs ...workflowobservability.Attribute) {}

func (s *fakeSpan) SetAttributes(attrs ...workflowobservability.Attribute) {
	for _, attr := range attrs {
		s.attrs[attr.Key] = attr.Value
	}
}

func (s *fakeSpan) RecordError(err error) {}

func (s *fakeSpan) SetError(message string) {}

type fakeTracer struct {
	span *fakeSpan
}

func (t *fakeTracer) Start(ctx context.Context, name string, options workflowobservability.SpanOptions) (context.Context, workflowobservability.Span) {
	return ctx, t.span
}

func (t *fakeTracer) ExtractTraceContext(ctx context.Context) map[string]string { return nil }

func TestStartExecutorProcessEmitsExecutorType(t *testing.T) {
	span := &fakeSpan{attrs: map[string]any{}}
	telemetry := observability.New(observability.Options{Tracer: &fakeTracer{span: span}})

	_, activity := telemetry.StartExecutorProcess(context.Background(), "exec1", "pkg.Type", "standard", nil, nil)
	if activity == nil {
		t.Fatal("expected an activity span")
	}

	// The executor type is emitted under the canonical OTel attribute
	// executor.type, matching the .NET (Tags.ExecutorType) and Python
	// (EXECUTOR_TYPE) implementations for cross-SDK dashboard alignment.
	if got := span.attrs["executor.type"]; got != "pkg.Type" {
		t.Errorf("executor.type = %v, want %q", got, "pkg.Type")
	}
	if _, ok := span.attrs["executor.implementation.id"]; ok {
		t.Error("span must not carry the non-canonical executor.implementation.id attribute")
	}
}
