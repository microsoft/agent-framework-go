// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"iter"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func captureExecutor(id string, sink *[]string, mu *sync.Mutex) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[string]()).YieldsOutputType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					mu.Lock()
					*sink = append(*sink, id+":"+msg.(string))
					mu.Unlock()
					return nil, ctx.SendMessage("", msg)
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}

func TestDirectEdgeRouting_NoCondition_DeliversAlways(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := captureExecutor("a", &trace, &mu)
	b := captureExecutor("b", &trace, &mu)
	wf, err := workflow.NewBuilder(a).AddEdge(a, b).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"a:test", "b:test"}
	if !slices.Equal(trace, want) {
		t.Errorf("trace = %v, want %v", trace, want)
	}
}

func TestDirectEdgeRouting_ConditionTrue_Delivers(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := captureExecutor("a", &trace, &mu)
	b := captureExecutor("b", &trace, &mu)
	cond := func(msg any) bool {
		s, ok := msg.(string)
		return ok && s == "match"
	}
	wf, err := workflow.NewBuilder(a).
		AddDirectEdge(a, b, false, cond).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "match"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"a:match", "b:match"}
	if !slices.Equal(trace, want) {
		t.Errorf("trace = %v, want %v", trace, want)
	}
}

func TestDirectEdgeRouting_ConditionFalse_DoesNotDeliver(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := captureExecutor("a", &trace, &mu)
	b := captureExecutor("b", &trace, &mu)
	cond := func(msg any) bool { return false }
	wf, err := workflow.NewBuilder(a).
		AddDirectEdge(a, b, false, cond).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"a:test"}
	if !slices.Equal(trace, want) {
		t.Errorf("trace = %v, want %v", trace, want)
	}
}

func TestDirectEdgeRouting_TargetedMessageDeliversOnlyToMatchingSink(t *testing.T) {
	tests := []struct {
		name      string
		targetID  string
		wantTrace []string
	}{
		{name: "matching target", targetID: "b", wantTrace: []string{"a:test", "b:test"}},
		{name: "non-matching target", targetID: "a", wantTrace: []string{"a:test"}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				mu    sync.Mutex
				trace []string
			)
			a := targetingExecutor("a", testCase.targetID, &trace, &mu)
			b := captureExecutor("b", &trace, &mu)
			wf, err := workflow.NewBuilder(a).AddEdge(a, b).Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if _, err := inproc.Default.Run(context.Background(), wf, "test"); err != nil {
				t.Fatalf("Run: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(trace, testCase.wantTrace) {
				t.Fatalf("trace = %v, want %v", trace, testCase.wantTrace)
			}
		})
	}
}

func TestDirectEdgeRouting_BroadDeclaredSendTypeRoutesByRuntimeType(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	source := workflow.ExecutorBinding{
		ID:               "source",
		ImplementationID: "*workflow.Executor",
	}
	source.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: source.ID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[any]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					return nil, ctx.SendMessage("", msg)
				})
				return rb, nil
			},
		}, nil
	}
	target := captureExecutor("target", &trace, &mu)

	wf, err := workflow.NewBuilder(source).
		AddEdge(source, target).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"target:hello"}
	if !slices.Equal(trace, want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}
}

func TestFanOutEdgeRouting_NoAssigner_DeliversToAll(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := captureExecutor("a", &trace, &mu)
	b := captureExecutor("b", &trace, &mu)
	c := captureExecutor("c", &trace, &mu)
	wf, err := workflow.NewBuilder(a).
		AddFanOutEdge(a, []workflow.ExecutorBinding{b, c}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	got := slices.Clone(trace)
	slices.Sort(got)
	want := []string{"a:test", "b:test", "c:test"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
}

func TestFanOutEdgeRouting_TargetedMessageDeliversOnlyToMatchingSink(t *testing.T) {
	tests := []struct {
		name      string
		targetID  string
		wantTrace []string
	}{
		{name: "matching first sink", targetID: "b", wantTrace: []string{"a:test", "b:test"}},
		{name: "matching second sink", targetID: "c", wantTrace: []string{"a:test", "c:test"}},
		{name: "non-matching target", targetID: "a", wantTrace: []string{"a:test"}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				mu    sync.Mutex
				trace []string
			)
			a := targetingExecutor("a", testCase.targetID, &trace, &mu)
			b := captureExecutor("b", &trace, &mu)
			c := captureExecutor("c", &trace, &mu)
			wf, err := workflow.NewBuilder(a).
				AddFanOutEdge(a, []workflow.ExecutorBinding{b, c}).
				Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if _, err := inproc.Default.Run(context.Background(), wf, "test"); err != nil {
				t.Fatalf("Run: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(trace, testCase.wantTrace) {
				t.Fatalf("trace = %v, want %v", trace, testCase.wantTrace)
			}
		})
	}
}

func TestFanOutEdgeRouting_AssignerSelectsSubset(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := captureExecutor("a", &trace, &mu)
	b := captureExecutor("b", &trace, &mu)
	c := captureExecutor("c", &trace, &mu)
	assigner := func(_ int, _ any) iter.Seq[int] {
		return func(yield func(int) bool) { yield(0) }
	}
	wf, err := workflow.NewBuilder(a).
		AddFanOutEdge(a, []workflow.ExecutorBinding{b, c}, workflow.WithEdgeAssigner(assigner)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, ent := range trace {
		if ent == "c:test" {
			t.Errorf("c should not have been selected; trace=%v", trace)
		}
	}
	want := []string{"a:test", "b:test"}
	got := slices.Clone(trace)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("trace = %v, want %v", got, want)
	}
}

func TestFanOutEdgeRouting_AssignerSelectsEmpty_NoDelivery(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := captureExecutor("a", &trace, &mu)
	b := captureExecutor("b", &trace, &mu)
	c := captureExecutor("c", &trace, &mu)
	assigner := func(_ int, _ any) iter.Seq[int] {
		return func(yield func(int) bool) {}
	}
	wf, err := workflow.NewBuilder(a).
		AddFanOutEdge(a, []workflow.ExecutorBinding{b, c}, workflow.WithEdgeAssigner(assigner)).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := inproc.Default.Run(context.Background(), wf, "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"a:test"}
	if !slices.Equal(trace, want) {
		t.Errorf("trace = %v, want %v", trace, want)
	}
}

func targetingExecutor(id string, targetID string, sink *[]string, mu *sync.Mutex) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					mu.Lock()
					*sink = append(*sink, id+":"+msg.(string))
					mu.Unlock()
					return nil, ctx.SendMessage(targetID, msg)
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}

func TestFanInEdgeRouting_StateResetsAfterDelivery(t *testing.T) {
	var (
		mu    sync.Mutex
		trace []string
	)
	a := captureExecutor("a", &trace, &mu)
	b := captureExecutor("b", &trace, &mu)
	c := captureExecutor("c", &trace, &mu)
	starter := captureExecutor("starter", &trace, &mu)

	wf, err := workflow.NewBuilder(starter).
		AddFanOutEdge(starter, []workflow.ExecutorBinding{a, b}).
		AddFanInBarrierEdge([]workflow.ExecutorBinding{a, b}, c).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := inproc.Default.Run(context.Background(), wf, "round1"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	if !slices.Contains(trace, "c:round1") {
		t.Errorf("expected c to receive round1 after both branches fired; trace=%v", trace)
	}
	mu.Unlock()
}

func emitsExecutor(id string, value string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, _ any) (any, error) {
					return nil, ctx.SendMessage("", value)
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}

func collectingExecutor(id string, deliveries *[][]string, mu *sync.Mutex) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					mu.Lock()
					*deliveries = append(*deliveries, []string{msg.(string)})
					mu.Unlock()
					return nil, nil
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}

func TestFanInBarrier_DeliversOnlyAfterAllSourcesProduce(t *testing.T) {
	source1 := emitsExecutor("executor1", "part1")
	source2 := emitsExecutor("executor2", "part2")

	var (
		mu         sync.Mutex
		deliveries [][]string
	)
	target := collectingExecutor("executor3", &deliveries, &mu)

	starter := emitsExecutor("starter", "kick")
	wf, err := workflow.NewBuilder(starter).
		AddFanOutEdge(starter, []workflow.ExecutorBinding{source1, source2}).
		AddFanInBarrierEdge([]workflow.ExecutorBinding{source1, source2}, target).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := inproc.Default.Run(context.Background(), wf, "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(deliveries) != 2 {
		t.Fatalf("expected 2 deliveries to executor3, got %d (%v)", len(deliveries), deliveries)
	}
	got := []string{deliveries[0][0], deliveries[1][0]}
	slices.Sort(got)
	want := []string{"part1", "part2"}
	if !slices.Equal(got, want) {
		t.Errorf("delivered messages = %v, want %v", got, want)
	}
}

func outputFilterExecutor(id string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[string]()).YieldsOutputType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					s := msg.(string)
					if err := ctx.YieldOutput("out:" + id + ":" + s); err != nil {
						return nil, err
					}
					return nil, ctx.SendMessage("", s)
				})
				return rb, nil
			},
		}, nil
	}
	return binding
}

func runWorkflowAndCollect(t *testing.T, wf *workflow.Workflow, input any) []workflow.OutputEvent {
	t.Helper()
	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var outs []workflow.OutputEvent
	for evt := range run.OutgoingEvents() {
		if o, ok := evt.(workflow.OutputEvent); ok {
			outs = append(outs, o)
		}
	}
	return outs
}

func TestOutputFilter_AllowsRegisteredExecutor(t *testing.T) {
	start := outputFilterExecutor("start")
	end := outputFilterExecutor("end")

	wf, err := workflow.NewBuilder(start).
		AddEdge(start, end).
		WithOutputFrom(end).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	outs := runWorkflowAndCollect(t, wf, "hello")
	if len(outs) != 1 {
		t.Fatalf("expected 1 OutputEvent from registered executor, got %d", len(outs))
	}
	if outs[0].ExecutorID != "end" {
		t.Errorf("ExecutorID = %q, want %q", outs[0].ExecutorID, "end")
	}
	if got, want := outs[0].Output, any("out:end:hello"); got != want {
		t.Errorf("Output = %v, want %v", got, want)
	}
}

func TestOutputFilter_RejectsUnregisteredExecutor(t *testing.T) {
	start := outputFilterExecutor("start")
	end := outputFilterExecutor("end")

	wf, err := workflow.NewBuilder(start).
		AddEdge(start, end).
		WithOutputFrom(end).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	outs := runWorkflowAndCollect(t, wf, "hello")
	for _, o := range outs {
		if o.ExecutorID == "start" {
			t.Errorf("output from unregistered executor 'start' was not filtered: %+v", o)
		}
	}
}

func TestOutputFilter_NoOutputExecutorsRegistered(t *testing.T) {
	start := outputFilterExecutor("start")

	wf, err := workflow.NewBuilder(start).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	outs := runWorkflowAndCollect(t, wf, "hello")
	if len(outs) != 0 {
		t.Errorf("expected 0 OutputEvents, got %d: %+v", len(outs), outs)
	}
}
