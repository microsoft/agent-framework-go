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

func aggregatorBinding(id string, results *[]string) *workflow.ExecutorBinding {
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.IsSharedInstance = true
	cache := &workflow.StatefulExecutorCache[string]{
		StateKey:            "aggregate",
		ScopeName:           "aggregate-scope",
		InitialStateFactory: func() string { return "" },
	}
	binding.Reset = func() bool {
		_ = cache.Reset()
		return true
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
						s := msg.(string)
						return nil, cache.InvokeWithState(ctx, false, func(_ *workflow.Context, state string) (string, error) {
							var newState string
							if state == "" {
								newState = s
							} else {
								newState = state + "+" + s
							}
							*results = append(*results, newState)
							return newState, nil
						})
					}), nil
				},
			}},
		}, nil
	}
	return binding
}

func TestStatefulExecutorCache_AggregatesIncrementally(t *testing.T) {
	var got []string
	binding := aggregatorBinding("agg", &got)
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	for _, in := range []string{"a", "b", "c"} {
		if err := stream.SendMessage(ctx, in); err != nil {
			t.Fatalf("SendMessage(%q): %v", in, err)
		}
	}

	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		_ = evt
	}

	want := []string{"a", "a+b", "a+b+c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("aggregation = %v, want %v", got, want)
	}
}

func TestStatefulExecutorCache_ResetRestartsAggregate(t *testing.T) {
	var got []string
	binding := aggregatorBinding("agg", &got)
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "a")
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if _, err := run.Resume(ctx, "b"); err != nil {
		t.Fatalf("first Resume: %v", err)
	}
	if err := run.Close(ctx); err != nil {
		t.Fatalf("Close first run: %v", err)
	}

	run, err = inproc.Default.Run(ctx, wf, "c")
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if err := run.Close(ctx); err != nil {
		t.Fatalf("Close second run: %v", err)
	}

	want := []string{"a", "a+b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("aggregation = %v, want %v", got, want)
	}
}

func TestStatefulExecutorCache_NilStateRestartsAggregate(t *testing.T) {
	var got []string
	binding := nullableAggregatorBinding("agg", &got)
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	for _, input := range []string{"a", "clear", "b"} {
		if err := stream.SendMessage(ctx, input); err != nil {
			t.Fatalf("SendMessage(%q): %v", input, err)
		}
	}
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		_ = evt
	}

	want := []string{"a", "<nil>", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("aggregation = %v, want %v", got, want)
	}
}

func nullableAggregatorBinding(id string, results *[]string) *workflow.ExecutorBinding {
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	cache := &workflow.StatefulExecutorCache[*string]{
		StateKey:            "nullable-aggregate",
		ScopeName:           "aggregate-scope",
		InitialStateFactory: func() *string { return nil },
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
						input := msg.(string)
						return nil, cache.InvokeWithState(ctx, false, func(_ *workflow.Context, state *string) (*string, error) {
							if input == "clear" {
								*results = append(*results, "<nil>")
								return nil, nil
							}
							value := input
							if state != nil {
								value = *state + "+" + input
							}
							*results = append(*results, value)
							return &value, nil
						})
					}), nil
				},
			}},
		}, nil
	}
	return binding
}

func TestWorkflow_RejectsReuseSharedExecutorWithoutReset(t *testing.T) {
	binding := &workflow.ExecutorBinding{
		ID:               "shared",
		ExecutorType:     reflect.TypeFor[*workflow.Executor](),
		IsSharedInstance: true,
		NewExecutor: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "shared",
				Config: []*workflow.ExecutorConfig{{
					ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
						return rb.AddHandler(reflect.TypeFor[string](), nil, false, func(_ *workflow.Context, _ any) (any, error) {
							return nil, nil
						}), nil
					},
				}},
			}, nil
		},
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "first")
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := run.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = inproc.Default.Run(ctx, wf, "second")
	if err == nil {
		t.Fatal("expected second run to reject shared executor without Reset")
	}
	if !strings.Contains(err.Error(), "cannot reuse Workflow with shared Executor instances") {
		t.Fatalf("error = %v, want shared executor reuse error", err)
	}
}
