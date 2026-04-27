// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

type noOpExecutor struct {
	id string
}

func (n *noOpExecutor) NewExecutor(runID string) (*workflow.Executor, error) {
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

func (n *someOtherNoOpExecutor) NewExecutor(runID string) (*workflow.Executor, error) {
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
