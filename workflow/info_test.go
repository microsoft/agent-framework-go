// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"iter"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func source(i int) string { return "Source/" + itoa(i) }
func sink(i int) string   { return "Sink/" + itoa(i) }

func itoa(i int) string {
	switch i {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	case 4:
		return "4"
	case 5:
		return "5"
	}
	return "?"
}

func directEdge(src, dst string, cond func(any) bool) workflow.Edge {
	return workflow.Edge{
		Connection: workflow.EdgeConnection{SourceIDs: []string{src}, SinkIDs: []string{dst}},
		Condition:  cond,
	}
}

func fanOutEdge(src string, sinks []string, assigner func(int, any) iter.Seq[int]) workflow.Edge {
	return workflow.Edge{
		Connection: workflow.EdgeConnection{SourceIDs: []string{src}, SinkIDs: sinks},
		Assigner:   assigner,
	}
}

func fanInEdge(srcs []string, dst string) workflow.Edge {
	return workflow.Edge{
		Connection: workflow.EdgeConnection{SourceIDs: srcs, SinkIDs: []string{dst}},
	}
}

func runEdgeInfoMatch(t *testing.T, name string, edge, comparator workflow.Edge, expect bool) {
	t.Helper()
	info := workflow.NewEdgeInfo(edge)
	if got := info.Match(comparator); got != expect {
		t.Errorf("%s: Match = %v, want %v", name, got, expect)
	}
}

func TestEdgeInfo_DirectEdges(t *testing.T) {
	always := func(any) bool { return true }

	d1 := directEdge(source(1), sink(2), nil)
	d1Copy := directEdge(source(1), sink(2), nil)
	d2 := directEdge(source(3), sink(4), nil)
	dCond := directEdge(source(3), sink(4), always)

	runEdgeInfoMatch(t, "self", d1, d1, true)
	runEdgeInfoMatch(t, "equal-no-cond", d1, d1Copy, true)
	runEdgeInfoMatch(t, "different-endpoints", d1, d2, false)
	runEdgeInfoMatch(t, "no-cond-vs-cond", d1Copy, dCond, false)
	runEdgeInfoMatch(t, "different-endpoints-and-cond", d1, dCond, false)
	runEdgeInfoMatch(t, "self-cond", dCond, dCond, true)
}

func TestEdgeInfo_FanOutEdges(t *testing.T) {
	assigner := func(int, any) iter.Seq[int] {
		return func(yield func(int) bool) {}
	}

	f1 := fanOutEdge(source(1), []string{sink(2), sink(3), sink(4)}, nil)
	f1Copy := fanOutEdge(source(1), []string{sink(2), sink(3), sink(4)}, nil)
	f1Reordered := fanOutEdge(source(1), []string{sink(3), sink(4), sink(2)}, nil)
	f1DiffSinks := fanOutEdge(source(1), []string{sink(2), sink(3), sink(5)}, nil)
	f1DiffSrc := fanOutEdge(source(2), []string{sink(2), sink(3), sink(4)}, nil)
	fAssigned := fanOutEdge(source(1), []string{sink(2), sink(3), sink(4)}, assigner)

	runEdgeInfoMatch(t, "self", f1, f1, true)
	runEdgeInfoMatch(t, "equal", f1, f1Copy, true)
	runEdgeInfoMatch(t, "reordered", f1, f1Reordered, false)
	runEdgeInfoMatch(t, "diff-sinks", f1, f1DiffSinks, false)
	runEdgeInfoMatch(t, "diff-source", f1, f1DiffSrc, false)
	runEdgeInfoMatch(t, "with-assigner-self", fAssigned, fAssigned, true)
	runEdgeInfoMatch(t, "no-assigner-vs-assigner", f1, fAssigned, false)
}

func TestEdgeInfo_FanInEdges(t *testing.T) {
	fi := fanInEdge([]string{source(1), source(2), source(3)}, sink(1))
	fiCopy := fanInEdge([]string{source(1), source(2), source(3)}, sink(1))
	fiReordered := fanInEdge([]string{source(2), source(3), source(1)}, sink(1))
	fiDiffSrcs := fanInEdge([]string{source(1), source(2), source(4)}, sink(1))
	fiDiffSink := fanInEdge([]string{source(1), source(2), source(3)}, sink(2))

	runEdgeInfoMatch(t, "self", fi, fi, true)
	runEdgeInfoMatch(t, "equal", fi, fiCopy, true)
	runEdgeInfoMatch(t, "reordered", fi, fiReordered, false)
	runEdgeInfoMatch(t, "diff-sources", fi, fiDiffSrcs, false)
	runEdgeInfoMatch(t, "diff-sink", fi, fiDiffSink, false)
}

func TestEdgeInfo_Label(t *testing.T) {
	e1 := workflow.Edge{Connection: workflow.EdgeConnection{SourceIDs: []string{"a"}, SinkIDs: []string{"b"}}}
	e2 := e1
	e2.Label = "labelled"
	runEdgeInfoMatch(t, "no-label-vs-label", e1, e2, false)

	e3 := e1
	e3.Label = "labelled"
	runEdgeInfoMatch(t, "same-label", e2, e3, true)
}

func TestReflectEdges_ReturnsEdgesPerSource(t *testing.T) {
	a := newNoOpExecutor("a")
	b := newNoOpExecutor("b")
	c := newNoOpExecutor("c")

	wf, err := workflow.NewBuilder(a).
		AddEdge(a, b, workflow.WithLabel("a→b")).
		AddEdge(b, c).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := wf.ReflectEdges()
	if len(got["a"]) != 1 {
		t.Fatalf("expected 1 edge from a, got %d", len(got["a"]))
	}
	if got["a"][0].Label != "a→b" {
		t.Errorf("a→b label = %q, want %q", got["a"][0].Label, "a→b")
	}
	if len(got["b"]) != 1 {
		t.Fatalf("expected 1 edge from b, got %d", len(got["b"]))
	}
}

func TestReflectExecutors_ReturnsCopy(t *testing.T) {
	a := newNoOpExecutor("a")
	b := newNoOpExecutor("b")
	wf, err := workflow.NewBuilder(a).AddEdge(a, b).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := wf.ReflectExecutors()
	if len(got) != 2 {
		t.Fatalf("expected 2 executors, got %d", len(got))
	}
	if _, ok := got["a"]; !ok {
		t.Errorf("missing executor a")
	}
	if _, ok := got["b"]; !ok {
		t.Errorf("missing executor b")
	}
	delete(got, "a")
	if _, ok := wf.ExecutorBindings["a"]; !ok {
		t.Errorf("ReflectExecutors did not return a copy: workflow lost binding 'a'")
	}
}

func TestRequestPortInfo_FieldsMatchSource(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "test-port",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	info := workflow.NewRequestPortInfo(port)
	if info.PortID != port.ID {
		t.Errorf("ID = %q, want %q", info.PortID, port.ID)
	}
	if !info.RequestType.Match(port.Request) {
		t.Errorf("RequestType does not match string")
	}
	if !info.ResponseType.Match(port.Response) {
		t.Errorf("ResponseType does not match int")
	}
}

func TestReflectPorts_ReturnsCopy(t *testing.T) {
	portBinding := workflow.BindRequestPort(workflow.RequestPort{
		ID:       "approval",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[bool](),
	})
	wf, err := workflow.NewBuilder(portBinding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := wf.ReflectPorts()
	if len(got) != 1 {
		t.Fatalf("expected 1 reflected port, got %d", len(got))
	}
	info, ok := got["approval"]
	if !ok {
		t.Fatalf("missing reflected port %q", "approval")
	}
	if info.PortID != "approval" {
		t.Fatalf("PortID = %q, want approval", info.PortID)
	}
	delete(got, "approval")
	if _, ok := wf.Ports["approval"]; !ok {
		t.Fatal("ReflectPorts did not return a copy: workflow lost port approval")
	}
}

func TestScopeID_Equality(t *testing.T) {
	privateScope1 := workflow.ScopeID{ExecutorID: "executor1"}
	privateScope2 := workflow.ScopeID{ExecutorID: "executor2"}
	if !privateScope1.Equal(workflow.ScopeID{ExecutorID: "executor1"}) {
		t.Fatal("private scope should equal same executor private scope")
	}
	if privateScope1.Equal(privateScope2) {
		t.Fatal("private scopes for different executors should not be equal")
	}

	sharedScope1 := workflow.ScopeID{ExecutorID: "executor1", ScopeName: "shared"}
	sharedScope2 := workflow.ScopeID{ExecutorID: "executor2", ScopeName: "shared"}
	if !sharedScope1.Equal(sharedScope2) {
		t.Fatal("shared scopes with same name should be equal regardless of executor")
	}
	if sharedScope1.Equal(workflow.ScopeID{ExecutorID: "executor1", ScopeName: "different"}) {
		t.Fatal("shared scopes with different names should not be equal")
	}
	if sharedScope1.Equal(privateScope1) {
		t.Fatal("shared scope should not equal private scope")
	}
}
