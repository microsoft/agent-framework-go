// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func TestExecutorSpec_ExtendProtocolAndLifecycleInOrder(t *testing.T) {
	var calls []string
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
	}
	spec := workflow.ExecutorSpec{
		DisableAutoSendMessageHandlerResultObject: true,
		DisableAutoYieldOutputHandlerResultObject: true,
	}
	spec.Extend(workflow.ExecutorSpec{
		Initialize: func(*workflow.Context) error {
			calls = append(calls, "initialize-1")
			return nil
		},
		Reset: func() error {
			calls = append(calls, "reset-1")
			return nil
		},
		OnCheckpoint: func(*workflow.Context) error {
			calls = append(calls, "checkpoint-1")
			return nil
		},
		OnCheckpointRestored: func(*workflow.Context) error {
			calls = append(calls, "restored-1")
			return nil
		},
		OnMessageDeliveryStarting: func(*workflow.Context) error {
			calls = append(calls, "starting-1")
			return nil
		},
		OnMessageDeliveryFinished: func(*workflow.Context) error {
			calls = append(calls, "finished-1")
			return nil
		},
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			calls = append(calls, "routes-1")
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*workflow.Context, any) (any, error) {
				calls = append(calls, "handler-string")
				return nil, nil
			})
			return rb, nil
		},
	})
	spec.Extend(workflow.ExecutorSpec{
		Initialize: func(*workflow.Context) error {
			calls = append(calls, "initialize-2")
			return nil
		},
		Reset: func() error {
			calls = append(calls, "reset-2")
			return nil
		},
		OnCheckpoint: func(*workflow.Context) error {
			calls = append(calls, "checkpoint-2")
			return nil
		},
		OnCheckpointRestored: func(*workflow.Context) error {
			calls = append(calls, "restored-2")
			return nil
		},
		OnMessageDeliveryStarting: func(*workflow.Context) error {
			calls = append(calls, "starting-2")
			return nil
		},
		OnMessageDeliveryFinished: func(*workflow.Context) error {
			calls = append(calls, "finished-2")
			return nil
		},
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			calls = append(calls, "routes-2")
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[int](), nil, func(*workflow.Context, any) (any, error) {
				calls = append(calls, "handler-int")
				return nil, nil
			})
			return rb, nil
		},
	})
	executor := &workflow.Executor{
		ID:   "composed",
		Spec: spec,
	}

	if err := executor.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := executor.OnCheckpoint(ctx); err != nil {
		t.Fatalf("OnCheckpoint: %v", err)
	}
	if err := executor.OnCheckpointRestored(ctx); err != nil {
		t.Fatalf("OnCheckpointRestored: %v", err)
	}
	if err := executor.OnMessageDeliveryStarting(ctx); err != nil {
		t.Fatalf("OnMessageDeliveryStarting: %v", err)
	}
	if _, err := executor.Execute(ctx, "input"); err != nil {
		t.Fatalf("Execute string: %v", err)
	}
	if _, err := executor.Execute(ctx, 1); err != nil {
		t.Fatalf("Execute int: %v", err)
	}
	if err := executor.OnMessageDeliveryFinished(ctx); err != nil {
		t.Fatalf("OnMessageDeliveryFinished: %v", err)
	}
	if err := executor.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	want := []string{
		"initialize-1", "initialize-2",
		"checkpoint-1", "checkpoint-2",
		"restored-1", "restored-2",
		"starting-1", "starting-2",
		"routes-1", "routes-2",
		"handler-string", "handler-int",
		"finished-1", "finished-2",
		"reset-1", "reset-2",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestExecutorSpec_ExtendFinishedRunsAllHooksAndReturnsFirstError(t *testing.T) {
	firstErr := errors.New("first")
	secondErr := errors.New("second")
	var calls []string
	spec := workflow.ExecutorSpec{}
	spec.Extend(workflow.ExecutorSpec{
		OnMessageDeliveryFinished: func(*workflow.Context) error {
			calls = append(calls, "first")
			return firstErr
		},
	})
	spec.Extend(workflow.ExecutorSpec{
		OnMessageDeliveryFinished: func(*workflow.Context) error {
			calls = append(calls, "second")
			return secondErr
		},
	})

	if err := spec.OnMessageDeliveryFinished(&workflow.Context{Context: t.Context()}); !errors.Is(err, firstErr) {
		t.Fatalf("OnMessageDeliveryFinished error = %v, want %v", err, firstErr)
	}
	want := []string{"first", "second"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestExecutorDescribeProtocol_RespectsAutoReturnOptions(t *testing.T) {
	inputType := reflect.TypeFor[executorProtocolInput]()
	outputType := reflect.TypeFor[executorProtocolOutput]()

	tests := []struct {
		name      string
		autoSend  bool
		autoYield bool
	}{
		{name: "send and yield", autoSend: true, autoYield: true},
		{name: "send only", autoSend: true, autoYield: false},
		{name: "yield only", autoSend: false, autoYield: true},
		{name: "neither", autoSend: false, autoYield: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			executor := &workflow.Executor{
				ID: "protocol",
				Spec: workflow.ExecutorSpec{
					DisableAutoSendMessageHandlerResultObject: !testCase.autoSend,
					DisableAutoYieldOutputHandlerResultObject: !testCase.autoYield,
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.RouteBuilder.AddHandlerRaw(inputType, outputType, func(*workflow.Context, any) (any, error) {
							return executorProtocolOutput{}, nil
						})
						return rb, nil
					},
				},
			}

			protocol := executor.DescribeProtocol()
			if !hasReflectType(protocol.Accepts, inputType) {
				t.Fatalf("Accepts = %v, want %v", protocol.Accepts, inputType)
			}
			if got := hasReflectType(protocol.Sends, outputType); got != testCase.autoSend {
				t.Fatalf("Sends contains output type = %v, want %v; sends=%v", got, testCase.autoSend, protocol.Sends)
			}
			if got := hasReflectType(protocol.Yields, outputType); got != testCase.autoYield {
				t.Fatalf("Yields contains output type = %v, want %v; yields=%v", got, testCase.autoYield, protocol.Yields)
			}
		})
	}
}

func TestExecutorDescribeProtocol_IncludesExplicitSendAndYieldTypes(t *testing.T) {
	explicitSend := reflect.TypeFor[protocolSent]()
	explicitYield := reflect.TypeFor[protocolYielded]()
	executor := &workflow.Executor{
		ID: "protocol",
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.
					SendsMessageType(explicitSend, nil, explicitSend).
					YieldsOutputType(explicitYield, nil, explicitYield)
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[executorProtocolInput](), reflect.TypeFor[executorProtocolOutput](), func(*workflow.Context, any) (any, error) {
					return executorProtocolOutput{}, nil
				})
				return rb, nil
			},
		},
	}

	protocol := executor.DescribeProtocol()
	if !reflect.DeepEqual(protocol.Sends, []reflect.Type{explicitSend}) {
		t.Fatalf("Sends = %v, want [%v]", protocol.Sends, explicitSend)
	}
	if !reflect.DeepEqual(protocol.Yields, []reflect.Type{explicitYield}) {
		t.Fatalf("Yields = %v, want [%v]", protocol.Yields, explicitYield)
	}
}

func TestExecutorDescribeProtocol_ValueReturnIsAllocFreeAfterBuild(t *testing.T) {
	executor := executorWithSendTypes(reflect.TypeFor[protocolSent]())
	_ = executor.DescribeProtocol()

	if allocations := testing.AllocsPerRun(1000, func() { _ = executor.DescribeProtocol() }); allocations != 0 {
		t.Fatalf("DescribeProtocol allocations = %v, want 0", allocations)
	}
}

type (
	executorProtocolInput  struct{}
	executorProtocolOutput struct{}
	protocolSent           struct{}
	protocolYielded        struct{}
)

func executorWithSendTypes(sendTypes ...reflect.Type) *workflow.Executor {
	return &workflow.Executor{
		ID: "send-protocol",
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(sendTypes...)
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*workflow.Context, any) (any, error) {
					return nil, nil
				})
				return rb, nil
			},
		},
	}
}

func hasReflectType(types []reflect.Type, typ reflect.Type) bool {
	for _, candidate := range types {
		if candidate == typ {
			return true
		}
	}
	return false
}

func TestBindExecutor_CopiesCrossRunShareable(t *testing.T) {
	resetCalled := false
	executor := &workflow.Executor{
		ID:                "shared",
		CrossRunShareable: true,
		Spec: workflow.ExecutorSpec{
			Reset: func() error {
				resetCalled = true
				return nil
			},
		},
	}

	binding := workflow.BindExecutor(executor)
	if !binding.IsSharedInstance {
		t.Fatal("BindExecutor returned non-shared binding")
	}
	if !binding.SupportsConcurrentSharedExecution {
		t.Fatal("SupportsConcurrentSharedExecution = false, want true from Executor.CrossRunShareable")
	}
	if binding.RawValue != executor {
		t.Fatalf("RawValue = %v, want bound executor", binding.RawValue)
	}
	if executor.ExecutorType != nil {
		t.Fatalf("ExecutorType = %v, want nil because BindExecutor should not mutate the shared instance", executor.ExecutorType)
	}
	got, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if got != executor {
		t.Fatal("CreateInstance did not return the shared executor instance")
	}
	if !binding.TryReset() {
		t.Fatal("TryReset = false, want true")
	}
	if !resetCalled {
		t.Fatal("TryReset did not call executor Reset")
	}
}

func TestBindExecutor_DefaultsToNonConcurrentSharedExecution(t *testing.T) {
	binding := workflow.BindExecutor(&workflow.Executor{ID: "shared"})
	if binding.SupportsConcurrentSharedExecution {
		t.Fatal("SupportsConcurrentSharedExecution = true, want false without Executor.CrossRunShareable")
	}

	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wf.AllowConcurrent() {
		t.Fatal("AllowConcurrent = true, want false for shared executor without CrossRunShareable")
	}
}

func TestAddHandlerRaw_WithHandlerOverwrite(t *testing.T) {
	executor := &workflow.Executor{
		ID: "overwrite-handler",
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.
					AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[string](), func(*workflow.Context, any) (any, error) {
						return "first", nil
					}).
					AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[string](), func(*workflow.Context, any) (any, error) {
						return "second", nil
					}, workflow.WithHandlerOverwrite(true))
				return rb, nil
			},
		},
	}
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
	}

	got, err := executor.Execute(ctx, "input")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "second" {
		t.Fatalf("Execute result = %v, want second", got)
	}
}

func TestAddCatchAll_WithHandlerOverwrite(t *testing.T) {
	executor := &workflow.Executor{
		ID: "overwrite-catch-all",
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.
					AddCatchAll(func(*workflow.Context, workflow.PortableValue) (any, error) {
						return "first", nil
					}).
					AddCatchAll(func(*workflow.Context, workflow.PortableValue) (any, error) {
						return "second", nil
					}, workflow.WithHandlerOverwrite(true))
				return rb, nil
			},
		},
	}
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
	}

	got, err := executor.Execute(ctx, "input")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "second" {
		t.Fatalf("Execute result = %v, want second", got)
	}
}

func TestExecutorExecute_HandlerPanicReportsFailure(t *testing.T) {
	executor := &workflow.Executor{
		ID: "panic-handler",
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*workflow.Context, any) (any, error) {
					panic("boom")
				})
				return rb, nil
			},
		},
	}
	var events []workflow.Event
	ctx := &workflow.Context{
		Context: t.Context(),
		AddEvent: func(evt workflow.Event) error {
			events = append(events, evt)
			return nil
		},
	}

	if _, err := executor.Execute(ctx, "input"); err == nil {
		t.Fatal("Execute returned nil error, want panic failure")
	}
	for _, evt := range events {
		if _, ok := evt.(workflow.ExecutorFailedEvent); ok {
			return
		}
	}
	t.Fatalf("events = %#v, want ExecutorFailedEvent", events)
}

func aggregatorBinding(id string, results *[]string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.IsSharedInstance = true
	cache := &workflow.StatefulExecutorCache[string]{
		StateKey:            "aggregate",
		ScopeName:           "aggregate-scope",
		InitialStateFactory: func() string { return "" },
	}
	binding.ResetFunc = func() bool {
		_ = cache.Reset()
		return true
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
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
					})
					return rb, nil
				},
			},
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

func nullableAggregatorBinding(id string, results *[]string) workflow.ExecutorBinding {
	binding := workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	cache := &workflow.StatefulExecutorCache[*string]{
		StateKey:            "nullable-aggregate",
		ScopeName:           "aggregate-scope",
		InitialStateFactory: func() *string { return nil },
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
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
					})
					return rb, nil
				},
			},
		}, nil
	}
	return binding
}

func TestWorkflow_AllowsReuseSharedExecutorWithoutResetWhenNoResettableExecutors(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID:               "shared",
		ExecutorType:     reflect.TypeFor[*workflow.Executor](),
		IsSharedInstance: true,
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "shared",
				Spec: workflow.ExecutorSpec{
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(_ *workflow.Context, _ any) (any, error) {
							return nil, nil
						})
						return rb, nil
					},
				},
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

	run, err = inproc.Default.Run(ctx, wf, "second")
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if err := run.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
