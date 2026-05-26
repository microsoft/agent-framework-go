// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
	"github.com/microsoft/agent-framework-go/workflow/internal/workflowtest"
	"github.com/microsoft/agent-framework-go/workflow/observability"
)

func TestObservability_CreatesWorkflowEndToEndSpans(t *testing.T) {
	tracer := workflowtest.NewRecordingTracer()
	wf := newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{})

	runWorkflow(t, inproc.Default, wf, "hello")

	spans := tracer.Spans()
	wantCounts := map[string]int{
		"workflow.build":     1,
		"workflow.session":   1,
		"workflow_invoke":    1,
		"edge_group.process": 2,
		"executor.process":   2,
		"message.send":       2,
	}
	for prefix, want := range wantCounts {
		if got := workflowtest.CountSpansWithPrefix(spans, prefix); got != want {
			t.Fatalf("span count for %q = %d, want %d; spans: %v", prefix, got, want, workflowtest.SpanNames(spans))
		}
	}

	runSpan := workflowtest.FindSpanWithPrefix(t, spans, "workflow_invoke")
	runSpan.RequireEvent(t, "workflow.started")
	runSpan.RequireEvent(t, "workflow.completed")
}

func TestObservability_CreatesWorkflowBuildSpan(t *testing.T) {
	tracer := workflowtest.NewRecordingTracer()
	_ = newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{})

	spans := tracer.Spans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1; spans: %v", len(spans), workflowtest.SpanNames(spans))
	}
	buildSpan := workflowtest.FindSpanWithPrefix(t, spans, "workflow.build")
	buildSpan.RequireEvent(t, "build.started")
	buildSpan.RequireEvent(t, "build.validation_completed")
	buildSpan.RequireEvent(t, "build.completed")
	buildSpan.RequireAttribute(t, "workflow.id")
	buildSpan.RequireAttribute(t, "workflow.definition")
	buildSpan.RequireEnded(t)
}

func TestObservability_DisabledByDefaultCreatesNoSpans(t *testing.T) {
	wf := newTelemetryWorkflow(t, nil, workflow.TelemetryOptions{})
	runWorkflow(t, inproc.Default, wf, "hello")
}

func TestObservability_DisableOptionsPreventSpecificSpans(t *testing.T) {
	tests := []struct {
		name          string
		options       workflow.TelemetryOptions
		absentPrefix  string
		presentPrefix string
	}{
		{
			name: "workflow build",
			options: workflow.TelemetryOptions{
				DisableWorkflowBuild: true,
			},
			absentPrefix:  "workflow.build",
			presentPrefix: "workflow_invoke",
		},
		{
			name: "workflow run",
			options: workflow.TelemetryOptions{
				DisableWorkflowRun: true,
			},
			absentPrefix:  "workflow_invoke",
			presentPrefix: "workflow.build",
		},
		{
			name: "executor process",
			options: workflow.TelemetryOptions{
				DisableExecutorProcess: true,
			},
			absentPrefix:  "executor.process",
			presentPrefix: "workflow_invoke",
		},
		{
			name: "edge group process",
			options: workflow.TelemetryOptions{
				DisableEdgeGroupProcess: true,
			},
			absentPrefix:  "edge_group.process",
			presentPrefix: "executor.process",
		},
		{
			name: "message send",
			options: workflow.TelemetryOptions{
				DisableMessageSend: true,
			},
			absentPrefix:  "message.send",
			presentPrefix: "executor.process",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tracer := workflowtest.NewRecordingTracer()
			wf := newTelemetryWorkflow(t, tracer, testCase.options)
			runWorkflow(t, inproc.Default, wf, "hello")

			spans := tracer.Spans()
			if got := workflowtest.CountSpansWithPrefix(spans, testCase.absentPrefix); got != 0 {
				t.Fatalf("span count for disabled prefix %q = %d, want 0; spans: %v", testCase.absentPrefix, got, workflowtest.SpanNames(spans))
			}
			if got := workflowtest.CountSpansWithPrefix(spans, testCase.presentPrefix); got == 0 {
				t.Fatalf("expected at least one span with prefix %q; spans: %v", testCase.presentPrefix, workflowtest.SpanNames(spans))
			}
			if testCase.absentPrefix == "workflow_invoke" {
				if got := workflowtest.CountSpansWithPrefix(spans, "workflow.session"); got != 0 {
					t.Fatalf("workflow.session span count = %d, want 0 when workflow run is disabled", got)
				}
			}
		})
	}
}

func TestObservability_DisableMessageSendStillPropagatesTraceContext(t *testing.T) {
	tracer := workflowtest.NewRecordingTracer()
	wf := newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{DisableMessageSend: true})

	runWorkflow(t, inproc.Default, wf, "hello")

	spans := tracer.Spans()
	if got := workflowtest.CountSpansWithPrefix(spans, "message.send"); got != 0 {
		t.Fatalf("message.send span count = %d, want 0", got)
	}
	reverseSpan := workflowtest.FindSpanWithPrefix(t, spans, "executor.process reverse")
	if len(reverseSpan.Options().SourceTraceContext) == 0 {
		t.Fatal("downstream executor span missing source trace context")
	}
}

func TestObservability_SensitiveDataControlsExecutorInputOutput(t *testing.T) {
	tests := []struct {
		name                string
		enableSensitiveData bool
		wantSensitiveAttrs  bool
	}{
		{name: "enabled", enableSensitiveData: true, wantSensitiveAttrs: true},
		{name: "disabled", enableSensitiveData: false, wantSensitiveAttrs: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tracer := workflowtest.NewRecordingTracer()
			wf := newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{EnableSensitiveData: testCase.enableSensitiveData})
			runWorkflow(t, inproc.Default, wf, "hello")

			executorSpan := workflowtest.FindSpanWithPrefix(t, tracer.Spans(), "executor.process upper")
			executorSpan.RequireOptionalStringAttribute(t, "executor.input", testCase.wantSensitiveAttrs, "hello")
			executorSpan.RequireOptionalStringAttribute(t, "executor.output", testCase.wantSensitiveAttrs, "HELLO")
		})
	}
}

func TestObservability_SensitiveDataControlsMessageContent(t *testing.T) {
	tests := []struct {
		name                string
		enableSensitiveData bool
		wantContent         bool
	}{
		{name: "enabled", enableSensitiveData: true, wantContent: true},
		{name: "disabled", enableSensitiveData: false, wantContent: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tracer := workflowtest.NewRecordingTracer()
			wf := newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{EnableSensitiveData: testCase.enableSensitiveData})
			runWorkflow(t, inproc.Default, wf, "hello")

			messageSpan := workflowtest.FindSpanWithPrefix(t, tracer.Spans(), "message.send")
			messageSpan.RequireAttribute(t, "message.source_id")
			messageSpan.RequireOptionalStringAttribute(t, "message.content", testCase.wantContent, "HELLO")
		})
	}
}

func TestObservability_RunSpansAreEnded(t *testing.T) {
	tests := []struct {
		name string
		env  *inproc.ExecutionEnvironment
	}{
		{name: "lockstep", env: inproc.Lockstep},
		{name: "off thread", env: inproc.OffThread},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			tracer := workflowtest.NewRecordingTracer()
			wf := newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{})
			runWorkflow(t, testCase.env, wf, "hello")

			spans := tracer.Spans()
			workflowtest.FindSpanWithPrefix(t, spans, "workflow.session").RequireEnded(t)
			workflowtest.FindSpanWithPrefix(t, spans, "workflow_invoke").RequireEnded(t)
		})
	}
}

func TestObservability_StreamingRunSpansAreEnded(t *testing.T) {
	tracer := workflowtest.NewRecordingTracer()
	wf := newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{})
	ctx := context.Background()
	stream, err := inproc.OffThread.RunStreaming(ctx, wf, "hello")
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	for _, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("WatchStream: %v", err)
		}
	}
	if err := stream.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	spans := tracer.Spans()
	workflowtest.FindSpanWithPrefix(t, spans, "workflow.session").RequireEnded(t)
	workflowtest.FindSpanWithPrefix(t, spans, "workflow_invoke").RequireEnded(t)
}

func TestObservability_StreamingMultiTurnRunSpansAreEnded(t *testing.T) {
	tracer := workflowtest.NewRecordingTracer()
	wf := newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{})
	ctx := context.Background()

	for _, input := range []string{"hello", "again"} {
		stream, err := inproc.OffThread.RunStreaming(ctx, wf, input)
		if err != nil {
			t.Fatalf("RunStreaming(%q): %v", input, err)
		}
		for _, err := range stream.WatchStream(ctx) {
			if err != nil {
				t.Fatalf("WatchStream(%q): %v", input, err)
			}
		}
		if err := stream.Close(ctx); err != nil {
			t.Fatalf("Close(%q): %v", input, err)
		}
	}

	spans := tracer.Spans()
	if got := workflowtest.CountSpansWithPrefix(spans, "workflow.session"); got != 2 {
		t.Fatalf("workflow.session span count = %d, want 2; spans: %v", got, workflowtest.SpanNames(spans))
	}
	if got := workflowtest.CountSpansWithPrefix(spans, "workflow_invoke"); got != 2 {
		t.Fatalf("workflow_invoke span count = %d, want 2; spans: %v", got, workflowtest.SpanNames(spans))
	}
	for _, span := range spans {
		if strings.HasPrefix(span.Name(), "workflow.session") || strings.HasPrefix(span.Name(), "workflow_invoke") {
			span.RequireEnded(t)
		}
	}
}

func TestObservability_AllSpansAreEndedAfterWorkflowCompletion(t *testing.T) {
	tracer := workflowtest.NewRecordingTracer()
	wf := newTelemetryWorkflow(t, tracer, workflow.TelemetryOptions{})
	runWorkflow(t, inproc.Lockstep, wf, "hello")

	for _, span := range tracer.Spans() {
		span.RequireEnded(t)
	}
}

func newTelemetryWorkflow(t *testing.T, tracer observability.Tracer, options workflow.TelemetryOptions) *workflow.Workflow {
	t.Helper()
	upper := workflow.NewExecutor("upper", func(input string) string {
		return strings.ToUpper(input)
	}).Bind()

	reverse := workflow.NewExecutor("reverse", func(input string) string {
		letters := []rune(input)
		for left, right := 0, len(letters)-1; left < right; left, right = left+1, right-1 {
			letters[left], letters[right] = letters[right], letters[left]
		}
		return string(letters)
	}).Bind()

	builder := workflow.NewBuilder(upper).
		AddEdge(upper, reverse).
		WithOutputFrom(reverse)
	if tracer != nil {
		builder.WithTelemetry(tracer, options)
	}
	wf, err := builder.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return wf
}

func runWorkflow(t *testing.T, env *inproc.ExecutionEnvironment, wf *workflow.Workflow, msg any) {
	t.Helper()
	ctx := context.Background()
	run, err := env.Run(ctx, wf, msg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := run.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
