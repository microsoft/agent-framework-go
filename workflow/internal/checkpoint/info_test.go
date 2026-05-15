// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func TestWorkflowInfoMatch_PreservesEdgeMultiplicity(t *testing.T) {
	bindings := map[string]workflow.ExecutorBinding{
		"a": {ID: "a", ExecutorType: reflect.TypeFor[struct{}]()},
		"b": {ID: "b", ExecutorType: reflect.TypeFor[struct{}]()},
		"c": {ID: "c", ExecutorType: reflect.TypeFor[struct{}]()},
	}
	recorded := &workflow.Workflow{
		StartExecutorID:  "a",
		ExecutorBindings: bindings,
		Edges: map[string][]workflow.Edge{
			"a": {
				{Connection: workflow.EdgeConnection{SourceIDs: []string{"a"}, SinkIDs: []string{"b"}}},
				{Connection: workflow.EdgeConnection{SourceIDs: []string{"a"}, SinkIDs: []string{"c"}}},
			},
		},
	}
	incompatible := &workflow.Workflow{
		StartExecutorID:  "a",
		ExecutorBindings: bindings,
		Edges: map[string][]workflow.Edge{
			"a": {
				{Connection: workflow.EdgeConnection{SourceIDs: []string{"a"}, SinkIDs: []string{"b"}}},
				{Connection: workflow.EdgeConnection{SourceIDs: []string{"a"}, SinkIDs: []string{"b"}}},
			},
		},
	}

	info := NewWorkflowInfo(recorded)
	if info.Match(incompatible) {
		t.Fatal("expected duplicate edge topology to be incompatible")
	}
}

func TestWorkflowInfoMatch_AllowsNilExecutorType(t *testing.T) {
	recorded := &workflow.Workflow{
		StartExecutorID: "a",
		ExecutorBindings: map[string]workflow.ExecutorBinding{
			"a": {ID: "a"},
		},
	}

	info := NewWorkflowInfo(recorded)
	if info.Match(recorded) {
		t.Fatal("expected nil executor type metadata not to match because empty TypeID is unknown")
	}

	incompatible := &workflow.Workflow{
		StartExecutorID: "a",
		ExecutorBindings: map[string]workflow.ExecutorBinding{
			"a": {ID: "a", ExecutorType: reflect.TypeFor[struct{}]()},
		},
	}
	if info.Match(incompatible) {
		t.Fatal("expected nil executor type metadata not to match concrete executor type")
	}
}
