// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func factoryConcurrentBinding(id string, sink *[]string, mu *sync.Mutex) *workflow.ExecutorBinding {
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
						mu.Lock()
						*sink = append(*sink, id+":"+msg.(string))
						mu.Unlock()
						return nil, ctx.SendMessage("", msg)
					}), nil
				},
			}},
		}, nil
	}
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func nonConcurrentBinding(id string) *workflow.ExecutorBinding {
	binding := factoryConcurrentBinding(id, new([]string), &sync.Mutex{})
	binding.SupportsConcurrentSharedExecution = false
	return binding
}

func TestAllowConcurrent_AllExecutorsConcurrent(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := factoryConcurrentBinding("a", &trace, &mu)
	b := factoryConcurrentBinding("b", &trace, &mu)
	wf, err := workflow.NewBuilder(a).AddEdge(a, b).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !wf.AllowConcurrent() {
		t.Errorf("AllowConcurrent = false, want true")
	}
}

func TestAllowConcurrent_AnyNonConcurrentExecutor(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := factoryConcurrentBinding("a", &trace, &mu)
	b := nonConcurrentBinding("b")
	wf, err := workflow.NewBuilder(a).AddEdge(a, b).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wf.AllowConcurrent() {
		t.Errorf("AllowConcurrent = true, want false")
	}
}

func TestInprocConcurrent_RunsAllConcurrentWorkflow(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := factoryConcurrentBinding("a", &trace, &mu)
	b := factoryConcurrentBinding("b", &trace, &mu)
	wf, err := workflow.NewBuilder(a).AddEdge(a, b).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Concurrent.Run(context.Background(), wf, "", "go"); err != nil {
		t.Fatalf("Concurrent.Run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(trace) != 2 || trace[0] != "a:go" || trace[1] != "b:go" {
		t.Errorf("trace = %v, want [a:go b:go]", trace)
	}
}

func TestInprocConcurrent_RejectsNonConcurrentWorkflow(t *testing.T) {
	a := nonConcurrentBinding("a")
	b := factoryConcurrentBinding("b", new([]string), &sync.Mutex{})
	wf, err := workflow.NewBuilder(a).AddEdge(a, b).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, err = inproc.Concurrent.Run(context.Background(), wf, "", "go")
	if err == nil {
		t.Fatalf("Concurrent.Run should reject a workflow with non-concurrent executors")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should mention the offending executor id 'a'; got %v", err)
	}
}

func TestInprocConcurrent_AcceptsAllConcurrentInStream(t *testing.T) {
	a := factoryConcurrentBinding("a", new([]string), &sync.Mutex{})
	wf, err := workflow.NewBuilder(a).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	stream, err := inproc.Concurrent.RunStreaming(context.Background(), wf, "")
	if err != nil {
		t.Fatalf("Concurrent.OpenStream: %v", err)
	}
	_ = stream.CancelRun()
}
