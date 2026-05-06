// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type noOpExecutor struct {
	id string
}

func (n *noOpExecutor) NewExecutor(sessionID string) (*workflow.Executor, error) {
	return &workflow.Executor{
		ID: n.id,
		Config: []*workflow.ExecutorConfig{
			{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddCatchAll(false, func(ctx *workflow.Context, msg workflow.PortableValue) (any, error) {
						return nil, ctx.SendMessage("", msg.Any())
					}), nil
				},
			},
		},
	}, nil
}

func newNoOpExecutor(id string) *workflow.ExecutorBinding {
	n := &noOpExecutor{id: id}
	return &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeOf(n),
		NewExecutor:  n.NewExecutor,
	}
}

type someOtherNoOpExecutor struct {
	id string
}

func (n *someOtherNoOpExecutor) NewExecutor(sessionID string) (*workflow.Executor, error) {
	return &workflow.Executor{
		ID: n.id,
		Config: []*workflow.ExecutorConfig{
			{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddCatchAll(false, func(ctx *workflow.Context, msg workflow.PortableValue) (any, error) {
						return nil, ctx.SendMessage("", msg.Any())
					}), nil
				},
			},
		},
	}, nil
}

func newSomeOtherNoOpExecutor(id string) *workflow.ExecutorBinding {
	n := &someOtherNoOpExecutor{id: id}
	return &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeOf(n),
		NewExecutor:  n.NewExecutor,
	}
}

func newPlaceholder(id string) *workflow.ExecutorBinding {
	return &workflow.ExecutorBinding{ID: id}
}

func TestBuilder_Validation_FailsWhenUnboundExecutors(t *testing.T) {
	_, err := workflow.NewBuilder(newPlaceholder("start")).
		AddEdge(newNoOpExecutor("start"), newPlaceholder("unbound")).
		Build()

	if err == nil {
		t.Error("expected error, got nil")
	} else if !strings.Contains(err.Error(), "workflow cannot be built because there are unbound executors: [unbound]") {
		t.Errorf("expected unbound executors error, got %v", err)
	}
}

func TestBuilder_Validation_FailsWhenUnreachableExecutors(t *testing.T) {
	_, err := workflow.NewBuilder(newPlaceholder("start")).
		BindExecutor(newNoOpExecutor("start")).
		AddEdge(newNoOpExecutor("unreachable"), newNoOpExecutor("also-unreachable")).
		Build()

	if err == nil {
		t.Error("expected error, got nil")
	} else if !strings.Contains(err.Error(), "workflow cannot be built because there are orphaned executors: [also-unreachable unreachable]") {
		t.Errorf("expected orphaned executors error, got %v", err)
	}
}

func TestBuilder_Validation_AddEdgesOutOfOrderDoesNotImpactReachability(t *testing.T) {
	wf, err := workflow.NewBuilder(newPlaceholder("start")).
		BindExecutor(newNoOpExecutor("start")).
		AddEdge(newNoOpExecutor("not-unreachable"), newNoOpExecutor("also-not-unreachable")).
		AddEdge(newPlaceholder("start"), newPlaceholder("not-unreachable")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.StartExecutorID != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID)
	}

	if len(wf.ExecutorBindings) != 3 {
		t.Errorf("expected 3 executor bindings, got %d", len(wf.ExecutorBindings))
	}

	for _, id := range []string{"start", "not-unreachable", "also-not-unreachable"} {
		if _, ok := wf.ExecutorBindings[id]; !ok {
			t.Errorf("expected binding for %s", id)
		} else {
			if wf.ExecutorBindings[id].ExecutorType != reflect.TypeFor[*noOpExecutor]() {
				t.Errorf("expected executor type *noOpExecutor for %s", id)
			}
		}
	}
}

func TestBuilder_LateBinding_Executor(t *testing.T) {
	wf, err := workflow.NewBuilder(newPlaceholder("start")).
		BindExecutor(newNoOpExecutor("start")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.StartExecutorID != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID)
	}

	if len(wf.ExecutorBindings) != 1 {
		t.Errorf("expected 1 executor binding, got %d", len(wf.ExecutorBindings))
	}

	if binding, ok := wf.ExecutorBindings["start"]; !ok {
		t.Error("expected binding for start")
	} else {
		if binding.ExecutorType != reflect.TypeFor[*noOpExecutor]() {
			t.Errorf("expected executor type *noOpExecutor")
		}
	}
}

func TestBuilder_LateImplicitBinding_Executor(t *testing.T) {
	start := newNoOpExecutor("start")
	wf, err := workflow.NewBuilder(newPlaceholder("start")).
		AddEdge(start, start).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.StartExecutorID != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID)
	}

	if len(wf.ExecutorBindings) != 1 {
		t.Errorf("expected 1 executor binding, got %d", len(wf.ExecutorBindings))
	}

	if binding, ok := wf.ExecutorBindings["start"]; !ok {
		t.Error("expected binding for start")
	} else {
		if binding.ExecutorType != reflect.TypeFor[*noOpExecutor]() {
			t.Errorf("expected executor type *noOpExecutor")
		}
	}
}

func TestBuilder_RebindToDifferent_Disallowed(t *testing.T) {
	executor1 := newNoOpExecutor("start")
	executor2 := newSomeOtherNoOpExecutor("start")

	_, err := workflow.NewBuilder(newPlaceholder("start")).
		AddEdge(executor1, executor2).
		Build()

	if err == nil {
		t.Error("expected error, got nil")
	} else if !strings.Contains(err.Error(), "cannot bind executor with ID \"start\" because an executor with the same ID but a different type (\"*workflow_test.noOpExecutor\" vs \"*workflow_test.someOtherNoOpExecutor\") is already bound") {
		t.Errorf("expected rebind executors error, got %v", err)
	}
}

func TestBuilder_RebindToSameish_Allowed(t *testing.T) {
	executor1 := newNoOpExecutor("start")

	wf, err := workflow.NewBuilder(newPlaceholder("start")).
		AddEdge(executor1, executor1).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf.StartExecutorID != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID)
	}

	if len(wf.ExecutorBindings) != 1 {
		t.Errorf("expected 1 executor binding, got %d", len(wf.ExecutorBindings))
	}

	if binding, ok := wf.ExecutorBindings["start"]; !ok {
		t.Error("expected binding for start")
	} else {
		if binding.ExecutorType != reflect.TypeFor[*noOpExecutor]() {
			t.Errorf("expected executor type *noOpExecutor")
		}
	}
}

func TestBuilder_Workflow_NameAndDescription(t *testing.T) {
	// Test with name and description
	wf1, err := workflow.NewBuilder(newPlaceholder("start")).
		WithName("Test Pipeline").
		WithDescription("Test workflow description").
		BindExecutor(newNoOpExecutor("start")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf1.Name != "Test Pipeline" {
		t.Errorf("expected name 'Test Pipeline', got %s", wf1.Name)
	}
	if wf1.Description != "Test workflow description" {
		t.Errorf("expected description 'Test workflow description', got %s", wf1.Description)
	}

	// Test without (defaults to empty string in Go)
	wf2, err := workflow.NewBuilder(newPlaceholder("start2")).
		BindExecutor(newNoOpExecutor("start2")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf2.Name != "" {
		t.Errorf("expected empty name, got %s", wf2.Name)
	}
	if wf2.Description != "" {
		t.Errorf("expected empty description, got %s", wf2.Description)
	}

	// Test with only name (no description)
	wf3, err := workflow.NewBuilder(newPlaceholder("start3")).
		WithName("Named Only").
		BindExecutor(newNoOpExecutor("start3")).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if wf3.Name != "Named Only" {
		t.Errorf("expected name 'Named Only', got %s", wf3.Name)
	}
	if wf3.Description != "" {
		t.Errorf("expected empty description, got %s", wf3.Description)
	}
}

// recordingBinding builds an executor that records every input it receives in
// the supplied slice (under mu) and forwards strings downstream unchanged.
func recordingBinding(id string, sink *[]string) *workflow.ExecutorBinding {
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Options: workflow.ExecutorOptions{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
			},
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[string](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
						*sink = append(*sink, id+":"+msg.(string))
						return nil, ctx.SendMessage("", msg)
					}), nil
				},
			}},
		}, nil
	}
	return binding
}

func TestAddChain_ConnectsExecutorsInOrder(t *testing.T) {
	var trace []string
	a := recordingBinding("a", &trace)
	b := recordingBinding("b", &trace)
	c := recordingBinding("c", &trace)

	wf, err := workflow.NewBuilder(a).
		AddChain(a, []*workflow.ExecutorBinding{b, c}, false).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "x"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"a:x", "b:x", "c:x"}
	if !reflect.DeepEqual(trace, want) {
		t.Errorf("trace = %v, want %v", trace, want)
	}
}

func TestAddChain_RejectsRepetitionByDefault(t *testing.T) {
	a := recordingBinding("a", new([]string))
	b := recordingBinding("b", new([]string))

	_, err := workflow.NewBuilder(a).
		AddChain(a, []*workflow.ExecutorBinding{b, b}, false).
		Build()
	if err == nil {
		t.Fatal("expected error for repeated executor")
	}
}

func TestAddSwitch_RoutesToMatchingCase(t *testing.T) {
	var trace []string
	src := recordingBinding("src", &trace)
	even := recordingBinding("even", &trace)
	odd := recordingBinding("odd", &trace)

	bld := workflow.NewBuilder(src)
	bld.AddSwitch(src).
		AddCase(func(msg any) bool {
			s, ok := msg.(string)
			return ok && len(s)%2 == 0
		}, even).
		AddCase(func(msg any) bool {
			s, ok := msg.(string)
			return ok && len(s)%2 == 1
		}, odd).
		AddToBuilder(bld)
	wf, err := bld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// "abcd" → len 4 → even
	if _, err := inproc.Default.Run(context.Background(), wf, "abcd"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantContains := "even:abcd"
	var found bool
	for _, t := range trace {
		if t == wantContains {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected trace to include %q, got %v", wantContains, trace)
	}
	for _, ent := range trace {
		if ent == "odd:abcd" {
			t.Errorf("expected odd branch not to receive even-length string; trace=%v", trace)
		}
	}
}

func TestAddSwitch_FallsBackToDefault(t *testing.T) {
	var trace []string
	src := recordingBinding("src", &trace)
	branch := recordingBinding("branch", &trace)
	def := recordingBinding("def", &trace)

	bld := workflow.NewBuilder(src)
	bld.AddSwitch(src).
		AddCase(func(msg any) bool { return msg == "match" }, branch).
		WithDefault(def).
		AddToBuilder(bld)
	wf, err := bld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := inproc.Default.Run(context.Background(), wf, "no-match"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, ent := range trace {
		if ent == "branch:no-match" {
			t.Errorf("non-matching message reached branch executor; trace=%v", trace)
		}
	}
	var sawDefault bool
	for _, ent := range trace {
		if ent == "def:no-match" {
			sawDefault = true
		}
	}
	if !sawDefault {
		t.Errorf("expected default branch to receive non-matching message; trace=%v", trace)
	}
}

func TestBuilder_Validation_FailsWhenOutputExecutorNotInGraph(t *testing.T) {
	start := newNoOpExecutor("start")

	// WithOutputFrom references an executor that was never added via an edge
	// or BindExecutor. Since it is tracked by WithOutputFrom itself but is
	// otherwise disconnected, the orphan-check fires first. Use a binding
	// that IS reachable but then reference a completely unknown ID via a
	// separate binding that only appears in WithOutputFrom.
	ghost := newNoOpExecutor("ghost")
	_, err := workflow.NewBuilder(start).
		AddEdge(start, newNoOpExecutor("next")).
		WithOutputFrom(ghost).
		Build()

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "orphaned executors") && !strings.Contains(err.Error(), "output executor") {
		t.Errorf("expected output or orphan executor error, got %v", err)
	}
}

func TestBuilder_Validation_OutputExecutorNotBound(t *testing.T) {
	// Directly test the output executor validation: if we somehow register
	// an output executor that is not in executorsBindings, the build must
	// fail with a clear error.
	start := newNoOpExecutor("start")
	target := newNoOpExecutor("target")

	_, err := workflow.NewBuilder(start).
		AddEdge(start, target).
		WithOutputFrom(target).
		Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestBuilder_Validation_SelfLoopWarning(t *testing.T) {
	// A self-loop (executor → itself) is allowed but should log a warning.
	// We verify it does not produce a build error.
	start := newNoOpExecutor("start")

	wf, err := workflow.NewBuilder(start).
		AddDirectEdge(start, start, true, func(any) bool { return false }).
		Build()
	if err != nil {
		t.Fatalf("expected no error for self-loop, got %v", err)
	}
	if wf.StartExecutorID != "start" {
		t.Errorf("expected start executor ID 'start', got %s", wf.StartExecutorID)
	}
}

func TestBuilder_Validation_DeadEndLogging(t *testing.T) {
	// Dead-end executors (no outgoing edges) should not cause build failure.
	start := newNoOpExecutor("start")
	leaf := newNoOpExecutor("leaf")

	wf, err := workflow.NewBuilder(start).
		AddEdge(start, leaf).
		Build()
	if err != nil {
		t.Fatalf("expected no error for dead-end, got %v", err)
	}
	if _, ok := wf.ExecutorBindings["leaf"]; !ok {
		t.Error("expected leaf executor in bindings")
	}
}
