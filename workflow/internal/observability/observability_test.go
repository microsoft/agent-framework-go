// Copyright (c) Microsoft. All rights reserved.

package observability_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	observability "github.com/microsoft/agent-framework-go/workflow/internal/observability"
	workflowobservability "github.com/microsoft/agent-framework-go/workflow/observability"
)

type unserializableValue struct{}

func (unserializableValue) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal failed")
}

func attributeValue(t *testing.T, attrs []workflowobservability.Attribute, key string) string {
	t.Helper()
	for _, attr := range attrs {
		if attr.Key == key {
			value, ok := attr.Value.(string)
			if !ok {
				t.Fatalf("attribute %q value is %T, want string", key, attr.Value)
			}
			return value
		}
	}
	t.Fatalf("attribute %q not found", key)
	return ""
}

func TestErrorAttributesShortTypeName(t *testing.T) {
	attrs := observability.ErrorAttributes(errors.New("boom"))
	if got := attributeValue(t, attrs, observability.TagErrorType); got != "errorString" {
		t.Errorf("error.type = %q, want %q", got, "errorString")
	}

	wrapped := fmt.Errorf("ctx: %w", errors.New("boom"))
	attrs = observability.ErrorAttributes(wrapped)
	// Assert the observable requirement (unqualified/short type name) rather than a
	// specific stdlib-internal type name, which is not a public API and may change.
	if got := attributeValue(t, attrs, observability.TagErrorType); got == "" || strings.ContainsAny(got, ".*") {
		t.Errorf("wrapped error.type = %q, want unqualified short type name", got)
	}
}

func TestBuildErrorAttributesShortTypeName(t *testing.T) {
	attrs := observability.BuildErrorAttributes(errors.New("boom"))
	if got := attributeValue(t, attrs, observability.TagBuildErrorType); got != "errorString" {
		t.Errorf("build.error.type = %q, want %q", got, "errorString")
	}

	wrapped := fmt.Errorf("ctx: %w", errors.New("boom"))
	attrs = observability.BuildErrorAttributes(wrapped)
	// Assert the observable requirement (unqualified/short type name) rather than a
	// specific stdlib-internal type name, which is not a public API and may change.
	if got := attributeValue(t, attrs, observability.TagBuildErrorType); got == "" || strings.ContainsAny(got, ".*") {
		t.Errorf("wrapped build.error.type = %q, want unqualified short type name", got)
	}
}

type fakeSpan struct {
	attrs []workflowobservability.Attribute
}

func (s *fakeSpan) End()                                                {}
func (s *fakeSpan) AddEvent(string, ...workflowobservability.Attribute) {}
func (s *fakeSpan) SetAttributes(attrs ...workflowobservability.Attribute) {
	s.attrs = append(s.attrs, attrs...)
}
func (s *fakeSpan) RecordError(error) {}
func (s *fakeSpan) SetError(string)   {}

type fakeTracer struct {
	span *fakeSpan
}

func (tr *fakeTracer) Start(ctx context.Context, _ string, _ workflowobservability.SpanOptions) (context.Context, workflowobservability.Span) {
	return ctx, tr.span
}
func (tr *fakeTracer) ExtractTraceContext(context.Context) map[string]string { return nil }

func TestCaptureErrorShortTypeName(t *testing.T) {
	span := &fakeSpan{}
	telemetry := observability.New(observability.Options{Tracer: &fakeTracer{span: span}})

	_, activity := telemetry.StartWorkflowRun(t.Context(), observability.WorkflowMetadata{ID: "wf"})
	activity.CaptureError(errors.New("boom"))

	if got := attributeValue(t, span.attrs, observability.TagErrorType); got != "errorString" {
		t.Errorf("captured error.type = %q, want %q", got, "errorString")
	}
}

func TestStartExecutorProcessEmitsExecutorType(t *testing.T) {
	span := &fakeSpan{}
	telemetry := observability.New(observability.Options{Tracer: &fakeTracer{span: span}})

	_, activity := telemetry.StartExecutorProcess(context.Background(), "exec1", "pkg.Type", "standard", nil, nil)
	if activity == nil {
		t.Fatal("expected an activity span")
	}

	// The executor type is emitted under the canonical OTel attribute
	// executor.type, matching the .NET (Tags.ExecutorType) and Python
	// (EXECUTOR_TYPE) implementations for cross-SDK dashboard alignment.
	if got := attributeValue(t, span.attrs, "executor.type"); got != "pkg.Type" {
		t.Errorf("executor.type = %v, want %q", got, "pkg.Type")
	}
	for _, attr := range span.attrs {
		if attr.Key == "executor.implementation.id" {
			t.Error("span must not carry the non-canonical executor.implementation.id attribute")
		}
	}
}

func TestSerializedAttributeUsesFallbackForMarshalErrors(t *testing.T) {
	attr := observability.SerializedAttribute("message.content", unserializableValue{})
	value, ok := attr.Value.(string)
	if !ok {
		t.Fatalf("attribute value type = %T, want string", attr.Value)
	}
	want := "[Unserializable: observability_test.unserializableValue]"
	if value != want {
		t.Fatalf("attribute value = %q, want %q", value, want)
	}
}

func TestSensitiveDataUsesFallbackForExecutorInputAndOutput(t *testing.T) {
	span := &fakeSpan{}
	telemetry := observability.New(observability.Options{
		Tracer:              &fakeTracer{span: span},
		EnableSensitiveData: true,
	})

	message := unserializableValue{}
	_, activity := telemetry.StartExecutorProcess(context.Background(), "exec1", "pkg.Type", "message", message, nil)
	if activity == nil {
		t.Fatal("expected an activity span")
	}
	telemetry.SetExecutorOutput(activity, message)

	want := "[Unserializable: observability_test.unserializableValue]"
	if got := attributeValue(t, span.attrs, observability.TagExecutorInput); got != want {
		t.Fatalf("executor.input = %q, want %q", got, want)
	}
	if got := attributeValue(t, span.attrs, observability.TagExecutorOutput); got != want {
		t.Fatalf("executor.output = %q, want %q", got, want)
	}
}
