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

func factoryConcurrentBinding(id string, sink *[]string, mu *sync.Mutex) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.SendsMessageType(reflect.TypeFor[string]())
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
						mu.Lock()
						*sink = append(*sink, id+":"+msg.(string))
						mu.Unlock()
						return nil, ctx.SendMessage("", msg)
					})
					return rb, nil
				},
			},
		}, nil
	}
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func nonConcurrentBinding(id string) workflow.ExecutorBinding {
	return workflow.BindExecutor(&workflow.Executor{
		ID: id,
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[string](), func(_ *workflow.Context, msg any) (any, error) {
					return msg, nil
				})
				return rb, nil
			},
		},
	})
}

func TestAllowConcurrent_BindFuncIsFactoryCreated(t *testing.T) {
	binding := workflow.BindFunc("upper", strings.ToUpper)
	executor, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if executor.CrossRunShareable {
		t.Fatal("BindFunc executor CrossRunShareable = true, want false to match .NET FunctionExecutor default")
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !wf.AllowConcurrent() {
		t.Fatal("AllowConcurrent = false, want true for BindFunc factory-created binding")
	}
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
	if _, err := inproc.Concurrent.Run(context.Background(), wf, "go"); err != nil {
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
	_, err = inproc.Concurrent.Run(context.Background(), wf, "go")
	if err == nil {
		t.Fatalf("Concurrent.Run should reject a workflow with non-concurrent executors")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should mention the offending executor id 'a'; got %v", err)
	}
}

func TestInprocConcurrent_RejectsWorkflowOwnedByAnotherRunner(t *testing.T) {
	a := factoryConcurrentBinding("a", new([]string), &sync.Mutex{})
	wf, err := workflow.NewBuilder(a).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.OffThread.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("OffThread.RunStreaming: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	_, err = inproc.Concurrent.RunStreaming(ctx, wf, nil)
	if err == nil {
		t.Fatal("Concurrent.RunStreaming should reject a workflow owned by another runner")
	}
	if !strings.Contains(err.Error(), "existing ownership") {
		t.Fatalf("error = %v, want ownership mismatch", err)
	}
}

func TestInprocConcurrent_AcceptsAllConcurrentInStream(t *testing.T) {
	a := factoryConcurrentBinding("a", new([]string), &sync.Mutex{})
	wf, err := workflow.NewBuilder(a).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	stream, err := inproc.Concurrent.RunStreaming(context.Background(), wf, nil)
	if err != nil {
		t.Fatalf("Concurrent.RunStreaming: %v", err)
	}
	_ = stream.CancelRun()
}
