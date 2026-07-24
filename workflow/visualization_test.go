// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

// buildVizWorkflow builds start -> {b, c} (fan-out) -> d (fan-in barrier) ->
// e (conditional, labeled) exercising every edge shape the renderers handle.
func buildVizWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	start := newNoOpExecutor("start")
	b := newNoOpExecutor("b")
	c := newNoOpExecutor("c")
	d := newNoOpExecutor("d")
	e := newNoOpExecutor("e")

	wf, err := workflow.NewBuilder(start).
		AddFanOutEdge(start, []workflow.ExecutorBinding{b, c}).
		AddFanInBarrierEdge([]workflow.ExecutorBinding{b, c}, d).
		AddDirectEdge(d, e, false, func(any) bool { return true }, workflow.WithEdgeLabel("approved")).
		WithOutputFrom(e).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return wf
}

func TestToMermaidString(t *testing.T) {
	got := workflow.ToMermaidString(buildVizWorkflow(t))

	wants := []string{
		"flowchart TD",
		"classDef startNode",     // highlight class declared
		"class start startNode;", // start node highlighted
		`start["start"]`,
		`b["b"]`,
		`c["c"]`,
		`d["d"]`,
		`e["e"]`,
		"start --> b", // fan-out expands to one edge per target
		"start --> c",
		`{{"fan-in"}}`, // synthesized fan-in junction node
		"fanin_b_c",    // junction id derived from sources
		"-.->",         // conditional edge is dashed
		"|approved|",   // edge label preserved
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("ToMermaidString output missing %q\n---\n%s", want, got)
		}
	}
}

func TestToDotString(t *testing.T) {
	got := workflow.ToDotString(buildVizWorkflow(t))

	wants := []string{
		"digraph Workflow {",
		`"start" [label="start", style=filled`, // start node highlighted
		`"b" [label="b"]`,
		`"c" [label="c"]`,
		`"d" [label="d"]`,
		`"e" [label="e"]`,
		"fan-in",             // junction label
		`label="approved"`,   // edge label preserved
		"style=dashed",       // conditional edge is dashed
		`"start" -> "b"`,     // fan-out edge
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("ToDotString output missing %q\n---\n%s", want, got)
		}
	}
}

func TestVisualization_NestedSubworkflow(t *testing.T) {
	childStart := newNoOpExecutor("child-start")
	child, err := workflow.NewBuilder(childStart).
		WithOutputFrom(childStart).
		Build()
	if err != nil {
		t.Fatalf("Build child: %v", err)
	}

	host := inproc.BindSubworkflowAsExecutor(child, "child")
	sink := newNoOpExecutor("sink")
	parent, err := workflow.NewBuilder(host).
		AddEdge(host, sink).
		WithOutputFrom(sink).
		Build()
	if err != nil {
		t.Fatalf("Build parent: %v", err)
	}

	mermaid := workflow.ToMermaidString(parent)
	for _, want := range []string{"subgraph child", "child-start"} {
		if !strings.Contains(mermaid, want) {
			t.Errorf("ToMermaidString nested output missing %q\n---\n%s", want, mermaid)
		}
	}

	dot := workflow.ToDotString(parent)
	for _, want := range []string{`subgraph "cluster_child"`, "child-start"} {
		if !strings.Contains(dot, want) {
			t.Errorf("ToDotString nested output missing %q\n---\n%s", want, dot)
		}
	}
}
