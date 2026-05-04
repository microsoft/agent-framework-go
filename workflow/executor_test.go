// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"context"
	"reflect"
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
