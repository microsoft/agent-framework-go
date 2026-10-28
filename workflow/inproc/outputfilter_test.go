// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func outputFilterWorkflow(t *testing.T, intermediate, accumulated bool) *workflow.Workflow {
	t.Helper()
	start := workflow.NewExecutor("start", func(ctx *workflow.Context, value any) error { return ctx.SendMessage("", value) }).Bind()
	end := workflow.NewExecutor("end", func(ctx *workflow.Context, value any) error { return ctx.SendMessage("", value) }).Bind()
	builder := workflow.NewBuilder(workflow.ExecutorBinding{ID: "start"}).AddEdge(start, end)
	if !intermediate || accumulated {
		builder.WithOutputFrom(end)
	}
	if intermediate {
		builder.WithIntermediateOutputFrom(end)
	}
	wf, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestOutputFilter_CanOutputRegisteredExecutor(t *testing.T) {
	filter := newOutputFilter(outputFilterWorkflow(t, false, false))
	if !filter.canOutput("end", "some output") {
		t.Fatal("registered end executor cannot output")
	}
}

func TestOutputFilter_CannotOutputUnregisteredExecutor(t *testing.T) {
	filter := newOutputFilter(outputFilterWorkflow(t, false, false))
	if filter.canOutput("start", "some output") {
		t.Fatal("unregistered start executor can output")
	}
}

func TestOutputFilter_CannotOutputNonexistentExecutor(t *testing.T) {
	filter := newOutputFilter(outputFilterWorkflow(t, false, false))
	if filter.canOutput("nonexistent", "some output") {
		t.Fatal("nonexistent executor can output")
	}
}

func TestOutputFilter_TerminalDesignationHasEmptyTags(t *testing.T) {
	filter := newOutputFilter(outputFilterWorkflow(t, false, false))
	tags, ok := filter.tryGetTags("end")
	if !ok || len(tags) != 0 {
		t.Fatalf("tags = %v, %t, want registered empty tags", tags, ok)
	}
}

func TestOutputFilter_IntermediateDesignationHasIntermediateTag(t *testing.T) {
	filter := newOutputFilter(outputFilterWorkflow(t, true, false))
	tags, ok := filter.tryGetTags("end")
	if !ok || !slices.Equal(tags, []workflow.OutputTag{workflow.OutputTagIntermediate}) {
		t.Fatalf("tags = %v, %t, want intermediate", tags, ok)
	}
}

func TestOutputFilter_AccumulatedDesignationHasIntermediateTag(t *testing.T) {
	filter := newOutputFilter(outputFilterWorkflow(t, true, true))
	tags, ok := filter.tryGetTags("end")
	if !ok || !slices.Equal(tags, []workflow.OutputTag{workflow.OutputTagIntermediate}) {
		t.Fatalf("tags = %v, %t, want intermediate", tags, ok)
	}
}

func TestOutputFilter_UnregisteredExecutorHasNoTags(t *testing.T) {
	filter := newOutputFilter(outputFilterWorkflow(t, false, false))
	tags, ok := filter.tryGetTags("start")
	if ok || tags != nil {
		t.Fatalf("tags = %v, %t, want nil and false", tags, ok)
	}
}
