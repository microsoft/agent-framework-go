// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func TestOutputTagIntermediate_HasExpectedValue(t *testing.T) {
	if got := workflow.OutputTagIntermediate.Value(); got != "intermediate" {
		t.Errorf("OutputTagIntermediate.Value() = %q, want %q", got, "intermediate")
	}
	if got := workflow.OutputTagIntermediate.String(); got != "intermediate" {
		t.Errorf("OutputTagIntermediate.String() = %q, want %q", got, "intermediate")
	}
}

func TestOutputEvent_HasTag(t *testing.T) {
	evt := workflow.OutputEvent{
		ExecutorID: "ex",
		Output:     "data",
		Tags:       []workflow.OutputTag{workflow.OutputTagIntermediate},
	}
	if !evt.HasTag(workflow.OutputTagIntermediate) {
		t.Error("HasTag(OutputTagIntermediate) = false, want true")
	}
}

func TestOutputEvent_NoTags_HasTagReturnsFalse(t *testing.T) {
	evt := workflow.OutputEvent{ExecutorID: "ex", Output: "data"}
	if evt.HasTag(workflow.OutputTagIntermediate) {
		t.Error("HasTag on untagged OutputEvent = true, want false")
	}
}

func TestWithOutputFrom_ProducesUntaggedOutputEvents(t *testing.T) {
	ex := echoBinding("ex-untagged")
	wf, err := workflow.NewBuilder(ex).WithOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tags, ok := wf.OutputExecutorTags("ex-untagged")
	if !ok {
		t.Fatal("OutputExecutorTags: executor not registered")
	}
	if len(tags) != 0 {
		t.Errorf("tags = %v, want empty (untagged)", tags)
	}
}

func TestWithIntermediateOutputFrom_ProducesIntermediateTag(t *testing.T) {
	ex := echoBinding("ex-intermediate")
	wf, err := workflow.NewBuilder(ex).WithIntermediateOutputFrom(ex).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tags, ok := wf.OutputExecutorTags("ex-intermediate")
	if !ok {
		t.Fatal("OutputExecutorTags: executor not registered")
	}
	if len(tags) != 1 || tags[0] != workflow.OutputTagIntermediate {
		t.Errorf("tags = %v, want [%v]", tags, workflow.OutputTagIntermediate)
	}
}

func TestWithTaggedOutputFrom_AccumulatesTags(t *testing.T) {
	ex := echoBinding("ex-tagged")
	wf, err := workflow.NewBuilder(ex).
		WithOutputFrom(ex).             // registers without tag
		WithIntermediateOutputFrom(ex). // adds intermediate tag
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tags, ok := wf.OutputExecutorTags("ex-tagged")
	if !ok {
		t.Fatal("OutputExecutorTags: executor not registered")
	}
	if len(tags) != 1 || tags[0] != workflow.OutputTagIntermediate {
		t.Errorf("tags = %v, want [%v]", tags, workflow.OutputTagIntermediate)
	}
}

func TestWithTaggedOutputFrom_DeduplicatesTags(t *testing.T) {
	ex := echoBinding("ex-dedup")
	wf, err := workflow.NewBuilder(ex).
		WithIntermediateOutputFrom(ex).
		WithIntermediateOutputFrom(ex). // duplicate
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tags, _ := wf.OutputExecutorTags("ex-dedup")
	if len(tags) != 1 {
		t.Errorf("deduplicated tags len = %d, want 1", len(tags))
	}
}

func echoBinding(id string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, _ any) (any, error) {
					return nil, ctx.YieldOutput("ok")
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}
