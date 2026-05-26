// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type attrInput struct{ Text string }

type attrOutput struct{ Text string }

type attrExecutor struct {
	_          workflow.AttrYieldsOutput[string]
	NamedYield workflow.AttrYieldsOutput[int]
	_          workflow.AttrSendsMessage[float64]
}

func (*attrExecutor) Handle(ctx *workflow.Context, input attrInput) (attrOutput, error) {
	if ctx == nil || ctx.Context == nil {
		panic("nil workflow context")
	}
	return attrOutput{Text: input.Text + "!"}, nil
}

type attrExecutorNoHandle struct {
	_ workflow.AttrYieldsOutput[string]
}

type simpleStructExecutor struct{}

func (simpleStructExecutor) Handle(input string) int { return len(input) }

type invalidStructExecutor struct{}

func (invalidStructExecutor) Handle(string, int) int { return 0 }

func expectNewExecutorPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("NewExecutor did not panic")
		}
	}()
	fn()
}

func TestNewExecutor_AcceptsExecutorPointer(t *testing.T) {
	ptr := &workflow.Executor{ID: "ptr", ImplementationID: "ptr"}
	if got := workflow.NewExecutor("ptr", ptr); got != ptr {
		t.Fatal("NewExecutor did not return the supplied executor pointer")
	}
}

func TestNewExecutor_AcceptsRequestPorts(t *testing.T) {
	port := workflow.RequestPort{ID: "port", Request: reflect.TypeFor[string](), Response: reflect.TypeFor[int]()}
	if got := workflow.NewExecutor("", port); got.ID != port.ID {
		t.Fatalf("NewExecutor(RequestPort).ID = %q, want %q", got.ID, port.ID)
	}
	if got := workflow.NewExecutor("", &port); got.ID != port.ID {
		t.Fatalf("NewExecutor(*RequestPort).ID = %q, want %q", got.ID, port.ID)
	}
}

func TestNewExecutor_AcceptsExecutorBinding(t *testing.T) {
	binding := workflow.BindNewExecutorFunc("binding", func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{ID: executorID}, nil
	})
	if got := workflow.NewExecutor("binding", binding); got.ID != binding.ID {
		t.Fatalf("NewExecutor(binding).ID = %q, want %q", got.ID, binding.ID)
	}
}

func TestNewExecutor_AcceptsStructPointerHandle(t *testing.T) {
	executor := workflow.NewExecutor("attrs-ptr", &attrExecutor{})
	if executor.ID != "attrs-ptr" {
		t.Fatalf("executor ID = %q, want attrs-ptr", executor.ID)
	}
	if executor.ImplementationID != "attrExecutor" {
		t.Fatalf("ImplementationID = %q, want attrExecutor", executor.ImplementationID)
	}
}

func TestNewExecutor_AcceptsStructHandleWithoutAttrs(t *testing.T) {
	executor := workflow.NewExecutor("simple-struct", simpleStructExecutor{})
	if executor.ImplementationID != "simpleStructExecutor" {
		t.Fatalf("ImplementationID = %q, want simpleStructExecutor", executor.ImplementationID)
	}
	ctx := &workflow.Context{Context: t.Context(), AddEvent: func(workflow.Event) error { return nil }, SendMessage: func(string, any) error { return nil }, YieldOutput: func(any) error { return nil }}
	result, err := executor.Execute(ctx, "abc")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != 3 {
		t.Fatalf("result = %v, want 3", result)
	}
}

func TestNewExecutor_AcceptsAnonymousStructWithEmbeddedHandle(t *testing.T) {
	source := struct{ simpleStructExecutor }{}
	executor := workflow.NewExecutor("embedded-struct", source)
	if want := reflect.TypeOf(source).String(); executor.ImplementationID != want {
		t.Fatalf("ImplementationID = %q, want %q", executor.ImplementationID, want)
	}
}

func TestNewExecutor_PanicsForInvalidInputs(t *testing.T) {
	var nilExecutor *workflow.Executor
	var nilPort *workflow.RequestPort
	var nilStruct *attrExecutor
	var nilFunc func(string)
	binding := workflow.ExecutorBinding{ID: "binding", ImplementationID: "binding", NewExecutorFunc: func(string) (*workflow.Executor, error) {
		return &workflow.Executor{ID: "binding"}, nil
	}}
	errorBinding := workflow.ExecutorBinding{ID: "error-binding", ImplementationID: "error-binding", NewExecutorFunc: func(string) (*workflow.Executor, error) {
		return nil, errors.New("boom")
	}}

	tests := []struct {
		name string
		fn   func()
	}{
		{name: "nil value", fn: func() { workflow.NewExecutor("nil", nil) }},
		{name: "nil executor pointer", fn: func() { workflow.NewExecutor("nil-executor", nilExecutor) }},
		{name: "executor pointer ID mismatch", fn: func() { workflow.NewExecutor("other", &workflow.Executor{ID: "executor"}) }},
		{name: "nil request port pointer", fn: func() { workflow.NewExecutor("nil-port", nilPort) }},
		{name: "binding ID mismatch", fn: func() { workflow.NewExecutor("other", binding) }},
		{name: "binding factory error", fn: func() { workflow.NewExecutor("error-binding", errorBinding) }},
		{name: "nil function", fn: func() { workflow.NewExecutor("nil-fn", nilFunc) }},
		{name: "unsupported scalar", fn: func() { workflow.NewExecutor("scalar", 1) }},
		{name: "unsupported struct", fn: func() { workflow.NewExecutor("struct", struct{}{}) }},
		{name: "nil struct pointer", fn: func() { workflow.NewExecutor("nil-struct", nilStruct) }},
		{name: "attrs without handle", fn: func() { workflow.NewExecutor("attrs-no-handle", attrExecutorNoHandle{}) }},
		{name: "invalid handle", fn: func() { workflow.NewExecutor("invalid-handle", invalidStructExecutor{}) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expectNewExecutorPanic(t, test.fn)
		})
	}
}

func TestNewExecutor_TypedActionFunction(t *testing.T) {
	var got string
	executor := workflow.NewExecutor("typed-action", func(input string) {
		got = input
	})
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
	}

	if _, err := executor.Execute(ctx, "hello"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "hello" {
		t.Fatalf("handler input = %q, want hello", got)
	}
}

func TestNewExecutor_TypedValueFunction(t *testing.T) {
	var sent any
	var yielded any
	executor := workflow.NewExecutor("typed-value", func(input string) int {
		return len(input)
	})
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
		SendMessage: func(_ string, message any) error {
			sent = message
			return nil
		},
		YieldOutput: func(output any) error {
			yielded = output
			return nil
		},
	}

	result, err := executor.Execute(ctx, "hello")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != 5 {
		t.Fatalf("result = %v, want 5", result)
	}
	if sent != 5 {
		t.Fatalf("sent message = %v, want 5", sent)
	}
	if yielded != 5 {
		t.Fatalf("yielded output = %v, want 5", yielded)
	}
}

func TestNewExecutor_TypedContextActionFunction(t *testing.T) {
	var got string
	executor := workflow.NewExecutor("typed-context-action", func(ctx *workflow.Context, input string) error {
		if ctx == nil || ctx.Context == nil {
			t.Fatal("handler received nil workflow context")
		}
		got = input
		return nil
	})
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
	}

	if _, err := executor.Execute(ctx, "hello"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "hello" {
		t.Fatalf("handler input = %q, want hello", got)
	}
}

func TestNewExecutor_TypedContextValueFunction(t *testing.T) {
	executor := workflow.NewExecutor("typed-context-value", func(ctx *workflow.Context, input string) (int, error) {
		if ctx == nil || ctx.Context == nil {
			t.Fatal("handler received nil workflow context")
		}
		return len(input), nil
	})
	ctx := &workflow.Context{
		Context:     t.Context(),
		AddEvent:    func(workflow.Event) error { return nil },
		SendMessage: func(string, any) error { return nil },
		YieldOutput: func(any) error { return nil },
	}

	result, err := executor.Execute(ctx, "hello")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != 5 {
		t.Fatalf("result = %v, want 5", result)
	}
}

func TestNewExecutor_StructHandleAndProtocolAttrs(t *testing.T) {
	executor := workflow.NewExecutor("attrs", attrExecutor{})
	descriptor := executor.DescribeProtocol()

	if !slices.Contains(descriptor.Accepts, reflect.TypeFor[attrInput]()) {
		t.Fatalf("Accepts = %v, want attrInput", descriptor.Accepts)
	}
	for _, want := range []reflect.Type{reflect.TypeFor[attrOutput](), reflect.TypeFor[float64]()} {
		if !slices.Contains(descriptor.Sends, want) {
			t.Fatalf("Sends = %v, want %v", descriptor.Sends, want)
		}
	}
	for _, want := range []reflect.Type{reflect.TypeFor[attrOutput](), reflect.TypeFor[string](), reflect.TypeFor[int]()} {
		if !slices.Contains(descriptor.Yields, want) {
			t.Fatalf("Yields = %v, want %v", descriptor.Yields, want)
		}
	}

	var sent []any
	var yielded []any
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
		SendMessage: func(_ string, message any) error {
			sent = append(sent, message)
			return nil
		},
		YieldOutput: func(output any) error {
			yielded = append(yielded, output)
			return nil
		},
	}

	result, err := executor.Execute(ctx, attrInput{Text: "go"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := attrOutput{Text: "go!"}
	if result != want {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	if !slices.ContainsFunc(sent, func(value any) bool { return value == want }) {
		t.Fatalf("sent = %#v, want %#v", sent, want)
	}
	if !slices.ContainsFunc(yielded, func(value any) bool { return value == want }) {
		t.Fatalf("yielded = %#v, want %#v", yielded, want)
	}
}

func TestNewExecutor_PanicsWhenContextIsNotFirstInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewExecutor did not panic")
		}
	}()
	workflow.NewExecutor("bad-context", func(string, *workflow.Context) {})
}

func TestNewExecutor_PanicsWhenFunctionHasMultipleInputs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewExecutor did not panic")
		}
	}()
	workflow.NewExecutor("multi-input", func(string, int) string { return "" })
}

func TestNewExecutor_PanicsWhenFunctionIsVariadic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewExecutor did not panic")
		}
	}()
	workflow.NewExecutor("variadic", func(input ...int) int { return len(input) })
}

func TestNewExecutor_PanicsWhenFunctionHasMultipleOutputs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewExecutor did not panic")
		}
	}()
	workflow.NewExecutor("multi-output", func(string) (int, string) { return 0, "" })
}

func TestNewExecutor_PanicsWhenNonFinalErrorCreatesMultipleOutputs(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewExecutor did not panic")
		}
	}()
	workflow.NewExecutor("error-output", func(string) (error, string) { return nil, "" }) //nolint:staticcheck // intentionally wrong signature to test panic
}

func TestExecutor_ExtendProtocolAndLifecycleInOrder(t *testing.T) {
	var calls []string
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
	}
	executor := workflow.Executor{
		ID: "composed",
		DisableAutoSendMessageHandlerResultObject: true,
		DisableAutoYieldOutputHandlerResultObject: true,
	}
	executor.Extend(&workflow.Executor{
		InitializeFunc: func(*workflow.Context) error {
			calls = append(calls, "initialize-1")
			return nil
		},
		ResetFunc: func() error {
			calls = append(calls, "reset-1")
			return nil
		},
		OnCheckpointFunc: func(*workflow.Context) error {
			calls = append(calls, "checkpoint-1")
			return nil
		},
		OnCheckpointRestoredFunc: func(*workflow.Context) error {
			calls = append(calls, "restored-1")
			return nil
		},
		OnMessageDeliveryStartingFunc: func(*workflow.Context) error {
			calls = append(calls, "starting-1")
			return nil
		},
		OnMessageDeliveryFinishedFunc: func(*workflow.Context) error {
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
	executor.Extend(&workflow.Executor{
		InitializeFunc: func(*workflow.Context) error {
			calls = append(calls, "initialize-2")
			return nil
		},
		ResetFunc: func() error {
			calls = append(calls, "reset-2")
			return nil
		},
		OnCheckpointFunc: func(*workflow.Context) error {
			calls = append(calls, "checkpoint-2")
			return nil
		},
		OnCheckpointRestoredFunc: func(*workflow.Context) error {
			calls = append(calls, "restored-2")
			return nil
		},
		OnMessageDeliveryStartingFunc: func(*workflow.Context) error {
			calls = append(calls, "starting-2")
			return nil
		},
		OnMessageDeliveryFinishedFunc: func(*workflow.Context) error {
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
	executorRef := &executor

	if err := executorRef.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := executorRef.OnCheckpoint(ctx); err != nil {
		t.Fatalf("OnCheckpoint: %v", err)
	}
	if err := executorRef.OnCheckpointRestored(ctx); err != nil {
		t.Fatalf("OnCheckpointRestored: %v", err)
	}
	if err := executorRef.OnMessageDeliveryStarting(ctx); err != nil {
		t.Fatalf("OnMessageDeliveryStarting: %v", err)
	}
	if _, err := executorRef.Execute(ctx, "input"); err != nil {
		t.Fatalf("Execute string: %v", err)
	}
	if _, err := executorRef.Execute(ctx, 1); err != nil {
		t.Fatalf("Execute int: %v", err)
	}
	if err := executorRef.OnMessageDeliveryFinished(ctx); err != nil {
		t.Fatalf("OnMessageDeliveryFinished: %v", err)
	}
	if err := executorRef.Reset(); err != nil {
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

func TestExecutor_ExtendReturnsReceiver(t *testing.T) {
	executor := &workflow.Executor{}
	if got := executor.Extend(&workflow.Executor{ResetFunc: func() error { return nil }}); got != executor {
		t.Fatal("Extend did not return receiver")
	}
}

func TestExecutor_ExtendFinishedRunsAllHooksAndReturnsFirstError(t *testing.T) {
	firstErr := errors.New("first")
	secondErr := errors.New("second")
	var calls []string
	executor := workflow.Executor{}
	executor.Extend(&workflow.Executor{
		OnMessageDeliveryFinishedFunc: func(*workflow.Context) error {
			calls = append(calls, "first")
			return firstErr
		},
	})
	executor.Extend(&workflow.Executor{
		OnMessageDeliveryFinishedFunc: func(*workflow.Context) error {
			calls = append(calls, "second")
			return secondErr
		},
	})

	if err := executor.OnMessageDeliveryFinished(&workflow.Context{Context: t.Context()}); !errors.Is(err, firstErr) {
		t.Fatalf("OnMessageDeliveryFinished error = %v, want %v", err, firstErr)
	}
	want := []string{"first", "second"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestExecutor_ExtendResetsCachedProtocol(t *testing.T) {
	ctx := &workflow.Context{
		Context:  t.Context(),
		AddEvent: func(workflow.Event) error { return nil },
	}
	executor := &workflow.Executor{
		ID: "extend-cache",
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*workflow.Context, any) (any, error) {
				return nil, nil
			})
			return rb, nil
		},
	}

	if _, err := executor.Execute(ctx, "before"); err != nil {
		t.Fatalf("Execute before Extend: %v", err)
	}
	executor.Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[int](), nil, func(*workflow.Context, any) (any, error) {
				return nil, nil
			})
			return rb, nil
		},
	})
	if _, err := executor.Execute(ctx, 1); err != nil {
		t.Fatalf("Execute after Extend: %v", err)
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

				DisableAutoSendMessageHandlerResultObject: !testCase.autoSend,
				DisableAutoYieldOutputHandlerResultObject: !testCase.autoYield,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddHandlerRaw(inputType, outputType, func(*workflow.Context, any) (any, error) {
						return executorProtocolOutput{}, nil
					})
					return rb, nil
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

		DisableAutoSendMessageHandlerResultObject: true,
		DisableAutoYieldOutputHandlerResultObject: true,
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(sendTypes...)
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*workflow.Context, any) (any, error) {
				return nil, nil
			})
			return rb, nil
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

func TestBindExecutor_RecordsImplementationID(t *testing.T) {
	executor := &workflow.Executor{ID: "shared", ImplementationID: "typed-shared"}

	binding := executor.Bind()
	if binding.ImplementationID != "typed-shared" {
		t.Fatalf("binding ImplementationID = %q, want %q", binding.ImplementationID, "typed-shared")
	}
	got, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if got != executor {
		t.Fatal("CreateInstance did not return the shared executor instance")
	}
}

func TestExecutor_SetCrossRunShareable(t *testing.T) {
	executor := &workflow.Executor{}
	if executor.CrossRunShareable {
		t.Fatal("CrossRunShareable = true, want false")
	}
	if got := executor.SetCrossRunShareable(true); got != executor {
		t.Fatal("SetCrossRunShareable did not return receiver")
	}
	if !executor.CrossRunShareable {
		t.Fatal("CrossRunShareable = false, want true")
	}
	executor.SetCrossRunShareable(false)
	if executor.CrossRunShareable {
		t.Fatal("CrossRunShareable = true, want false")
	}
}

func TestBindExecutor_RecordsCrossRunShareable(t *testing.T) {
	executor := &workflow.Executor{ID: "shared", ImplementationID: "typed-shared", CrossRunShareable: true}

	binding := executor.Bind()
	if !binding.SupportsConcurrentSharedExecution {
		t.Fatal("SupportsConcurrentSharedExecution = false, want true")
	}
}

func TestAddHandlerRaw_WithHandlerOverwrite(t *testing.T) {
	executor := &workflow.Executor{
		ID: "overwrite-handler",

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

		DisableAutoSendMessageHandlerResultObject: true,
		DisableAutoYieldOutputHandlerResultObject: true,
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*workflow.Context, any) (any, error) {
				panic("boom")
			})
			return rb, nil
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
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.SharedInstance = true
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
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	cache := &workflow.StatefulExecutorCache[*string]{
		StateKey:            "nullable-aggregate",
		ScopeName:           "aggregate-scope",
		InitialStateFactory: func() *string { return nil },
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,

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
		}, nil
	}
	return binding
}

func TestWorkflow_AllowsReuseSharedExecutorWithoutResetWhenNoResettableExecutors(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID:               "shared",
		ImplementationID: "*workflow.Executor",
		SharedInstance:   true,
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "shared",

				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(_ *workflow.Context, _ any) (any, error) {
						return nil, nil
					})
					return rb, nil
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
