// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"encoding/json"
	"strings"
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

func TestWorkflowInfoMatch_RequiresOutputTagsToMatch(t *testing.T) {
	start := testBinding("a", "test")
	terminal, err := workflow.NewBuilder(start).
		WithOutputFrom(start).
		Build()
	if err != nil {
		t.Fatalf("Build terminal: %v", err)
	}
	intermediate, err := workflow.NewBuilder(start).
		WithIntermediateOutputFrom(start).
		Build()
	if err != nil {
		t.Fatalf("Build intermediate: %v", err)
	}

	info := NewWorkflowInfo(intermediate)
	if !info.Match(intermediate) {
		t.Fatal("expected tagged workflow info to match original workflow")
	}
	if info.Match(terminal) {
		t.Fatal("expected tagged workflow info not to match terminal-only workflow")
	}
}

func TestWorkflowInfoJSON_RoundTripsTaggedOutputExecutors(t *testing.T) {
	start := testBinding("a", "test")
	wf, err := workflow.NewBuilder(start).
		WithIntermediateOutputFrom(start).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	data, err := json.Marshal(NewWorkflowInfo(wf))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "OutputExecutorIDs") {
		t.Fatalf("WorkflowInfo JSON should not include OutputExecutorIDs: %s", data)
	}
	var raw struct {
		OutputExecutors map[string][]workflow.OutputTag
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw: %v", err)
	}
	if tags := raw.OutputExecutors["a"]; len(tags) != 1 || tags[0] != workflow.OutputTagIntermediate {
		t.Fatalf("OutputExecutors[a] = %v, want [intermediate]", tags)
	}

	var got WorkflowInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal WorkflowInfo: %v", err)
	}
	if !got.Match(wf) {
		t.Fatal("expected JSON round-tripped WorkflowInfo to match workflow")
	}
}
