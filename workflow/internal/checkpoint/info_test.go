// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func testBinding(id string, typ reflect.Type) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: typ,
		NewExecutorFunc: func(string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID:           id,
				ExecutorType: typ,
				Spec: workflow.ExecutorSpec{
					ConfigureProtocol: func(builder *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						return builder, nil
					},
				},
			}, nil
		},
	}
}

func TestWorkflowInfoMatch_PreservesEdgeMultiplicity(t *testing.T) {
	a := testBinding("a", reflect.TypeFor[struct{}]())
	b := testBinding("b", reflect.TypeFor[struct{}]())
	c := testBinding("c", reflect.TypeFor[struct{}]())
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

func TestWorkflowInfoMatch_AllowsNilExecutorType(t *testing.T) {
	recorded, err := workflow.NewBuilder(testBinding("a", nil)).Build()
	if err != nil {
		t.Fatalf("Build recorded: %v", err)
	}

	info := NewWorkflowInfo(recorded)
	if info.Match(recorded) {
		t.Fatal("expected nil executor type metadata not to match because empty TypeID is unknown")
	}

	incompatible, err := workflow.NewBuilder(testBinding("a", reflect.TypeFor[struct{}]())).Build()
	if err != nil {
		t.Fatalf("Build incompatible: %v", err)
	}
	if info.Match(incompatible) {
		t.Fatal("expected nil executor type metadata not to match concrete executor type")
	}
}
