// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func testBinding(id string, implementationID string) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: implementationID,
		NewExecutorFunc: func(string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: id,

				ConfigureProtocol: func(builder *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					return builder, nil
				},
			}, nil
		},
	}
}

func TestWorkflowInfoMatch_PreservesEdgeMultiplicity(t *testing.T) {
	a := testBinding("a", "test")
	b := testBinding("b", "test")
	c := testBinding("c", "test")
	recorded, err := workflow.NewBuilder(a).
		AddEdge(a, b).
		AddEdge(a, c).
		Build()
	if err != nil {
		t.Fatalf("Build recorded: %v", err)
	}
	incompatible, err := workflow.NewBuilder(a).
		AddEdge(a, b).
		AddDirectEdge(a, b, false, func(any) bool { return true }).
		Build()
	if err != nil {
		t.Fatalf("Build incompatible: %v", err)
	}

	info := NewWorkflowInfo(recorded)
	if info.Match(incompatible) {
		t.Fatal("expected duplicate edge topology to be incompatible")
	}
}

func TestWorkflowInfoMatch_UsesInferredImplementationID(t *testing.T) {
	wf, err := workflow.NewBuilder(testBinding("a", "")).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	info := NewWorkflowInfo(wf)
	if !info.Match(wf) {
		t.Fatal("expected workflow info to match workflow with inferred implementation ID")
	}
	if got := info.Executors["a"].ImplementationID; got != "a" {
		t.Fatalf("ImplementationID = %q, want a", got)
	}
}
