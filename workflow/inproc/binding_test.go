// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type (
	textMessage struct{ Text string }
	dataMessage struct{ Bytes []byte }
)

func namedExecutorFunc3Implementation(ctx *workflow.Context, in textMessage) error {
	return ctx.SendMessage("", dataMessage{Bytes: []byte(in.Text)})
}

func namedExecutorFunc1Implementation(_ textMessage) {}

func namedExecutorFunc2Implementation(in textMessage) dataMessage {
	return dataMessage{Bytes: []byte(in.Text)}
}

func namedExecutorFuncImplementation(_ *workflow.Context, in textMessage) (dataMessage, error) {
	return dataMessage{Bytes: []byte(in.Text)}, nil
}

func namedNewExecutorFactory(string) (*workflow.Executor, error) {
	return &workflow.Executor{ID: "factory"}, nil
}

func namedBoundNewExecutorFactory(_ string, executorID string) (*workflow.Executor, error) {
	return &workflow.Executor{ID: executorID}, nil
}

func TestNewExecutor_ContextActionImplementationIDUsesNamedFunction(t *testing.T) {
	executor := workflow.NewExecutor("fallback", namedExecutorFunc3Implementation)
	if executor.ImplementationID == "fallback" {
		t.Fatalf("ImplementationID = %q, want named function", executor.ImplementationID)
	}
	if !strings.HasSuffix(executor.ImplementationID, ".namedExecutorFunc3Implementation") {
		t.Fatalf("ImplementationID = %q, want suffix .namedExecutorFunc3Implementation", executor.ImplementationID)
	}
}

func TestNewExecutor_ContextActionImplementationIDUsesIDForAnonymousFunction(t *testing.T) {
	executor := workflow.NewExecutor("anonymous", func(_ *workflow.Context, _ textMessage) error {
		return nil
	})
	if executor.ImplementationID != "anonymous" {
		t.Fatalf("ImplementationID = %q, want anonymous", executor.ImplementationID)
	}
}

func TestNewExecutor_ActionImplementationIDUsesNamedFunction(t *testing.T) {
	executor := workflow.NewExecutor("fallback", namedExecutorFunc1Implementation)
	if executor.ImplementationID == "fallback" {
		t.Fatalf("ImplementationID = %q, want named function", executor.ImplementationID)
	}
	if !strings.HasSuffix(executor.ImplementationID, ".namedExecutorFunc1Implementation") {
		t.Fatalf("ImplementationID = %q, want suffix .namedExecutorFunc1Implementation", executor.ImplementationID)
	}
}

func TestNewExecutor_ActionImplementationIDUsesIDForAnonymousFunction(t *testing.T) {
	executor := workflow.NewExecutor("anonymous", func(_ textMessage) {})
	if executor.ImplementationID != "anonymous" {
		t.Fatalf("ImplementationID = %q, want anonymous", executor.ImplementationID)
	}
}

func TestNewExecutor_ValueImplementationIDUsesNamedFunction(t *testing.T) {
	executor := workflow.NewExecutor("fallback", namedExecutorFunc2Implementation)
	if executor.ImplementationID == "fallback" {
		t.Fatalf("ImplementationID = %q, want named function", executor.ImplementationID)
	}
	if !strings.HasSuffix(executor.ImplementationID, ".namedExecutorFunc2Implementation") {
		t.Fatalf("ImplementationID = %q, want suffix .namedExecutorFunc2Implementation", executor.ImplementationID)
	}
}

func TestNewExecutor_ValueImplementationIDUsesIDForAnonymousFunction(t *testing.T) {
	executor := workflow.NewExecutor("anonymous", func(in textMessage) dataMessage {
		return dataMessage{Bytes: []byte(in.Text)}
	})
	if executor.ImplementationID != "anonymous" {
		t.Fatalf("ImplementationID = %q, want anonymous", executor.ImplementationID)
	}
}

func TestNewExecutor_ContextValueImplementationIDUsesNamedFunction(t *testing.T) {
	executor := workflow.NewExecutor("fallback", namedExecutorFuncImplementation)
	if executor.ImplementationID == "fallback" {
		t.Fatalf("ImplementationID = %q, want named function", executor.ImplementationID)
	}
	if !strings.HasSuffix(executor.ImplementationID, ".namedExecutorFuncImplementation") {
		t.Fatalf("ImplementationID = %q, want suffix .namedExecutorFuncImplementation", executor.ImplementationID)
	}
}

func TestNewExecutor_ContextValueImplementationIDUsesIDForAnonymousFunction(t *testing.T) {
	executor := workflow.NewExecutor("anonymous", func(_ *workflow.Context, in textMessage) (dataMessage, error) {
		return dataMessage{Bytes: []byte(in.Text)}, nil
	})
	if executor.ImplementationID != "anonymous" {
		t.Fatalf("ImplementationID = %q, want anonymous", executor.ImplementationID)
	}
}

func TestExecutorBinding_ImplementationIDUsesNamedFactory(t *testing.T) {
	binding := workflow.ExecutorBinding{ID: "factory", NewExecutorFunc: namedNewExecutorFactory}
	executor, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if executor.ImplementationID == "factory" {
		t.Fatalf("ImplementationID = %q, want named function", executor.ImplementationID)
	}
	if !strings.HasSuffix(executor.ImplementationID, ".namedNewExecutorFactory") {
		t.Fatalf("ImplementationID = %q, want suffix .namedNewExecutorFactory", executor.ImplementationID)
	}
}

func TestExecutorBinding_ImplementationIDUsesIDForAnonymousFactory(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID: "anonymous-factory",
		NewExecutorFunc: func(string) (*workflow.Executor, error) {
			return &workflow.Executor{ID: "anonymous-factory"}, nil
		},
	}
	executor, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if executor.ImplementationID != "anonymous-factory" {
		t.Fatalf("ImplementationID = %q, want anonymous-factory", executor.ImplementationID)
	}
}

func TestBindNewExecutorFunc_RecordsFactoryBinding(t *testing.T) {
	binding := workflow.BindNewExecutorFunc("factory", namedBoundNewExecutorFactory)
	if binding.ID != "factory" {
		t.Fatalf("ID = %q, want factory", binding.ID)
	}
	if binding.SharedInstance {
		t.Fatal("SharedInstance = true, want false")
	}
	if binding.SupportsConcurrentSharedExecution {
		t.Fatal("SupportsConcurrentSharedExecution = true, want false")
	}
	if !binding.TryReset() {
		t.Fatal("TryReset = false, want true for non-shared factory binding")
	}
	if binding.ImplementationID == "factory" {
		t.Fatalf("ImplementationID = %q, want named function", binding.ImplementationID)
	}
	if !strings.HasSuffix(binding.ImplementationID, ".namedBoundNewExecutorFactory") {
		t.Fatalf("ImplementationID = %q, want suffix .namedBoundNewExecutorFactory", binding.ImplementationID)
	}

	executor, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if executor.ImplementationID != binding.ImplementationID {
		t.Fatalf("executor ImplementationID = %q, want %q", executor.ImplementationID, binding.ImplementationID)
	}
}

func TestBindNewExecutorFunc_StampsAnonymousFactoryIdentity(t *testing.T) {
	var gotSessionID, gotExecutorID string
	binding := workflow.BindNewExecutorFunc("anonymous-factory", func(sessionID string, executorID string) (*workflow.Executor, error) {
		gotSessionID = sessionID
		gotExecutorID = executorID
		return workflow.NewExecutor(executorID, namedExecutorFunc1Implementation), nil
	})
	executor, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if gotSessionID != "session" {
		t.Fatalf("sessionID = %q, want session", gotSessionID)
	}
	if gotExecutorID != "anonymous-factory" {
		t.Fatalf("executorID = %q, want anonymous-factory", gotExecutorID)
	}
	if executor.ImplementationID != "anonymous-factory" {
		t.Fatalf("executor ImplementationID = %q, want anonymous-factory", executor.ImplementationID)
	}
}

func TestBindNewExecutorFunc_RejectsMismatchedExecutorID(t *testing.T) {
	binding := workflow.BindNewExecutorFunc("factory", func(string, string) (*workflow.Executor, error) {
		return &workflow.Executor{ID: "other"}, nil
	})
	_, err := binding.NewExecutorFunc("session")
	if err == nil {
		t.Fatal("NewExecutorFunc returned nil error")
	}
	if !strings.Contains(err.Error(), `Executor ID mismatch: expected "factory", but got "other"`) {
		t.Fatalf("error = %q, want executor ID mismatch", err)
	}
}

func TestBindNewExecutorFunc_PanicsOnNilFactory(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("BindNewExecutorFunc did not panic")
		}
	}()
	workflow.BindNewExecutorFunc("factory", nil)
}

func TestFunctionStyleBindings_DefaultToNonConcurrent(t *testing.T) {
	tests := []struct {
		name    string
		binding workflow.ExecutorBinding
	}{
		{
			name:    "NewExecutor func(T)",
			binding: workflow.NewExecutor("fn0", func(_ textMessage) {}).Bind(),
		},
		{
			name: "NewExecutor func(T) U",
			binding: workflow.NewExecutor("fn1", func(in textMessage) dataMessage {
				return dataMessage{Bytes: []byte(in.Text)}
			}).Bind(),
		},
		{
			name: "NewExecutor func(*Context, T) error",
			binding: workflow.NewExecutor("fn", func(_ *workflow.Context, _ textMessage) error {
				return nil
			}).Bind(),
		},
		{
			name: "NewExecutor func(*Context, T) (U, error)",
			binding: workflow.NewExecutor("fn-output", func(_ *workflow.Context, in textMessage) (dataMessage, error) {
				return dataMessage{Bytes: []byte(in.Text)}, nil
			}).Bind(),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.binding.SupportsConcurrentSharedExecution {
				t.Fatal("SupportsConcurrentSharedExecution = true, want false")
			}
		})
	}
}

func TestExecutorBinding_CreateInstanceStampsImplementationID(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID: "factory",
		NewExecutorFunc: func(string) (*workflow.Executor, error) {
			return &workflow.Executor{ID: "factory"}, nil
		},
	}
	executor, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if executor.ImplementationID != "factory" {
		t.Fatalf("executor ImplementationID = %q, want factory", executor.ImplementationID)
	}
}

func TestExecutorBinding_CreateInstanceInfersImplementationID(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID: "direct",
		NewExecutorFunc: func(string) (*workflow.Executor, error) {
			return &workflow.Executor{ID: "direct"}, nil
		},
	}
	executor, err := binding.CreateInstance("session")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if executor.ImplementationID != "direct" {
		t.Fatalf("executor ImplementationID = %q, want direct", executor.ImplementationID)
	}
}

func TestNewExecutor_ActionInvokesHandlerWithMessage(t *testing.T) {
	called := false
	binding := workflow.NewExecutor("fn0", func(in textMessage) {
		if in.Text != "hello" {
			t.Errorf("handler input = %q, want %q", in.Text, "hello")
		}
		called = true
	}).Bind()

	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "hello"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("handler was not invoked")
	}
}

func TestNewExecutor_ValueInvokesHandlerWithMessage(t *testing.T) {
	id := "fn1"
	binding := workflow.NewExecutor(id, func(in textMessage) dataMessage {
		return dataMessage{Bytes: []byte(in.Text)}
	}).Bind()

	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "abc"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out *workflow.OutputEvent
	for evt := range run.OutgoingEvents() {
		if e, ok := evt.(workflow.OutputEvent); ok {
			out = &e
		}
	}
	if out == nil {
		t.Fatal("expected an OutputEvent")
	}
	got, ok := out.Output.(dataMessage)
	if !ok {
		t.Fatalf("OutputEvent.Output type = %T, want dataMessage", out.Output)
	}
	if string(got.Bytes) != "abc" {
		t.Errorf("OutputEvent.Output bytes = %q, want %q", got.Bytes, "abc")
	}
}

func TestNewExecutor_ContextActionInvokesHandlerWithMessage(t *testing.T) {
	called := false
	binding := workflow.NewExecutor("fn", func(ctx *workflow.Context, in textMessage) error {
		if ctx == nil || ctx.Context == nil {
			t.Fatal("handler received nil workflow context")
		}
		if in.Text != "hello" {
			t.Errorf("handler input = %q, want %q", in.Text, "hello")
		}
		called = true
		return nil
	}).Bind()

	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "hello"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("handler was not invoked")
	}
}

func TestNewExecutor_ContextValueInvokesHandlerWithMessage(t *testing.T) {
	id := "fn"
	binding := workflow.NewExecutor(id, func(ctx *workflow.Context, in textMessage) (dataMessage, error) {
		if ctx == nil || ctx.Context == nil {
			t.Fatal("handler received nil workflow context")
		}
		return dataMessage{Bytes: []byte(in.Text)}, nil
	}).Bind()

	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "abc"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out *workflow.OutputEvent
	for evt := range run.OutgoingEvents() {
		if e, ok := evt.(workflow.OutputEvent); ok {
			out = &e
		}
	}
	if out == nil {
		t.Fatal("expected an OutputEvent")
	}
	got, ok := out.Output.(dataMessage)
	if !ok {
		t.Fatalf("OutputEvent.Output type = %T, want dataMessage", out.Output)
	}
	if string(got.Bytes) != "abc" {
		t.Errorf("OutputEvent.Output bytes = %q, want %q", got.Bytes, "abc")
	}
}

func TestActionHandler_SendsDeclaredMessageType(t *testing.T) {
	executor := &workflow.Executor{
		ID:               "message-handler",
		ImplementationID: "test.message-handler",
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[dataMessage]())
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[textMessage](), nil, func(ctx *workflow.Context, msg any) (any, error) {
				return nil, ctx.SendMessage("", dataMessage{Bytes: []byte(msg.(textMessage).Text)})
			})
			return rb, nil
		},
	}
	start := executor.Bind()
	sink := workflow.NewExecutor("sink", func(in dataMessage) textMessage {
		return textMessage{Text: string(in.Bytes)}
	}).Bind()

	wf, err := workflow.NewBuilder(start).
		AddEdge(start, sink).
		WithOutputFrom(sink).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "abc"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out *workflow.OutputEvent
	for evt := range run.OutgoingEvents() {
		if e, ok := evt.(workflow.OutputEvent); ok {
			out = &e
		}
	}
	if out == nil {
		t.Fatal("expected an OutputEvent")
	}
	got, ok := out.Output.(textMessage)
	if !ok {
		t.Fatalf("OutputEvent.Output type = %T, want textMessage", out.Output)
	}
	if got.Text != "abc" {
		t.Fatalf("OutputEvent.Output.Text = %q, want abc", got.Text)
	}
}

func TestActionHandler_YieldsDeclaredOutputType(t *testing.T) {
	executor := &workflow.Executor{
		ID:               "yield-handler",
		ImplementationID: "test.yield-handler",
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.YieldsOutputType(reflect.TypeFor[dataMessage]())
			rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[textMessage](), nil, func(ctx *workflow.Context, msg any) (any, error) {
				return nil, ctx.YieldOutput(dataMessage{Bytes: []byte(msg.(textMessage).Text)})
			})
			return rb, nil
		},
	}
	binding := executor.Bind()
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "abc"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var out *workflow.OutputEvent
	for evt := range run.OutgoingEvents() {
		if e, ok := evt.(workflow.OutputEvent); ok {
			out = &e
		}
	}
	if out == nil {
		t.Fatal("expected an OutputEvent")
	}
	got, ok := out.Output.(dataMessage)
	if !ok {
		t.Fatalf("OutputEvent.Output type = %T, want dataMessage", out.Output)
	}
	if string(got.Bytes) != "abc" {
		t.Fatalf("OutputEvent.Output.Bytes = %q, want abc", got.Bytes)
	}
}

func TestHandlerWithoutOutputTypeDoesNotAutoOutputReturnValue(t *testing.T) {
	binding := workflow.BindNewExecutorFunc("action", func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[textMessage](), nil, func(_ *workflow.Context, msg any) (any, error) {
					return dataMessage{Bytes: []byte(msg.(textMessage).Text)}, nil
				})
				return rb, nil
			},
		}, nil
	})
	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "abc"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	outputs := outputEvents(slices.Collect(run.OutgoingEvents()))
	if len(outputs) != 0 {
		t.Fatalf("output count = %d, want 0; outputs: %#v", len(outputs), outputs)
	}
}

func TestWorkflowOutput_AllowsValuesAssignableToDeclaredOutputType(t *testing.T) {
	tests := []struct {
		name   string
		output polymorphicOutput
	}{
		{name: "base", output: basePolymorphicOutput{}},
		{name: "derived", output: derivedPolymorphicOutput{}},
		{name: "grandchild", output: grandchildPolymorphicOutput{}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			binding := polymorphicOutputBinding("poly", testCase.output)
			wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			events := runAndCollectEvents(t, wf, "test input")
			outputs := outputEvents(events)
			if len(outputs) != 1 {
				t.Fatalf("output count = %d, want 1; events: %#v", len(outputs), events)
			}
			got, ok := outputs[0].Output.(polymorphicOutput)
			if !ok {
				t.Fatalf("OutputEvent.Output = %T, want polymorphicOutput", outputs[0].Output)
			}
			if got.OutputName() != testCase.output.OutputName() {
				t.Fatalf("OutputName() = %q, want %q", got.OutputName(), testCase.output.OutputName())
			}
			if errors := errorEvents(events); len(errors) != 0 {
				t.Fatalf("unexpected error events: %#v", errors)
			}
		})
	}
}

func TestWorkflowOutput_RejectsValueNotAssignableToDeclaredOutputType(t *testing.T) {
	binding := unrelatedOutputBinding("poly")
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	events := runAndCollectEvents(t, wf, "test input")
	errors := errorEvents(events)
	if len(errors) != 1 {
		t.Fatalf("error count = %d, want 1; events: %#v", len(errors), events)
	}
	message := errors[0].Error.Error()
	if !strings.Contains(message, "cannot output object of type") || !strings.Contains(message, "polymorphicOutput") {
		t.Fatalf("error = %q, want incompatible output type message", message)
	}
	if outputs := outputEvents(events); len(outputs) != 0 {
		t.Fatalf("output count = %d, want 0; outputs: %#v", len(outputs), outputs)
	}
}

func TestWorkflowOutput_AgentResponseUsesOutputFilter(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID:               "agent-output",
		ImplementationID: "*workflow.Executor",
		RawValue:         struct{}{},
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: binding.ID,

			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, _ any) (any, error) {
					return nil, wctx.YieldOutput(&agent.Response{
						Messages: []*message.Message{{
							Role:     message.RoleAssistant,
							Contents: message.Contents{&message.TextContent{Text: "agent output"}},
						}},
					})
				})
				return rb, nil
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	events := runAndCollectEvents(t, wf, "input")
	if errors := errorEvents(events); len(errors) != 0 {
		t.Fatalf("unexpected error events: %#v", errors)
	}
	if outputs := outputEvents(events); len(outputs) != 0 {
		t.Fatalf("output count = %d, want 0 without output designation; outputs: %#v", len(outputs), outputs)
	}

	wf, err = workflow.NewBuilder(binding).WithIntermediateOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build with output: %v", err)
	}
	events = runAndCollectEvents(t, wf, "input")
	if errors := errorEvents(events); len(errors) != 0 {
		t.Fatalf("unexpected error events with output: %#v", errors)
	}
	outputs := outputEvents(events)
	if len(outputs) != 1 {
		t.Fatalf("output count = %d, want 1 with output designation; outputs: %#v", len(outputs), outputs)
	}
	if _, ok := outputs[0].Output.(*agent.Response); !ok {
		t.Fatalf("OutputEvent.Output = %T, want *agent.Response", outputs[0].Output)
	}
	if !outputs[0].IsIntermediate() {
		t.Fatalf("OutputEvent tags = %v, want intermediate", outputs[0].Tags)
	}
}

func TestWorkflowOutput_ExplicitYieldTypesValidateContextYieldOutput(t *testing.T) {
	tests := []struct {
		name      string
		output    any
		wantError bool
	}{
		{name: "declared", output: dataMessage{Bytes: []byte("ok")}},
		{name: "undeclared", output: textMessage{Text: "no"}, wantError: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			binding := explicitYieldBinding("yield", testCase.output)
			wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			events := runAndCollectEvents(t, wf, "input")
			outputs := outputEvents(events)
			errors := errorEvents(events)
			if testCase.wantError {
				if len(errors) != 1 {
					t.Fatalf("error count = %d, want 1; events: %#v", len(errors), events)
				}
				if len(outputs) != 0 {
					t.Fatalf("output count = %d, want 0; outputs: %#v", len(outputs), outputs)
				}
				return
			}
			if len(errors) != 0 {
				t.Fatalf("unexpected error events: %#v", errors)
			}
			if len(outputs) != 1 {
				t.Fatalf("output count = %d, want 1; events: %#v", len(outputs), events)
			}
			if got, ok := outputs[0].Output.(dataMessage); !ok || string(got.Bytes) != "ok" {
				t.Fatalf("OutputEvent.Output = %#v, want dataMessage ok", outputs[0].Output)
			}
		})
	}
}

func TestWorkflowOutput_ExplicitYieldTypeAllowsPointerToDeclaredValueType(t *testing.T) {
	binding := explicitYieldBinding("yield", &dataMessage{Bytes: []byte("ok")})
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	events := runAndCollectEvents(t, wf, "input")
	if errors := errorEvents(events); len(errors) != 0 {
		t.Fatalf("unexpected error events: %#v", errors)
	}
	outputs := outputEvents(events)
	if len(outputs) != 1 {
		t.Fatalf("output count = %d, want 1; events: %#v", len(outputs), events)
	}
	got, ok := outputs[0].Output.(*dataMessage)
	if !ok || string(got.Bytes) != "ok" {
		t.Fatalf("OutputEvent.Output = %#v, want *dataMessage ok", outputs[0].Output)
	}
}

type polymorphicOutput interface {
	OutputName() string
}

type basePolymorphicOutput struct{}

func (basePolymorphicOutput) OutputName() string { return "base" }

type derivedPolymorphicOutput struct{}

func (derivedPolymorphicOutput) OutputName() string { return "derived" }

type grandchildPolymorphicOutput struct{}

func (grandchildPolymorphicOutput) OutputName() string { return "grandchild" }

type unrelatedOutput struct{}

func polymorphicOutputBinding(id string, output polymorphicOutput) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			DisableAutoSendMessageHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[polymorphicOutput](), func(_ *workflow.Context, _ any) (any, error) {
					return output, nil
				})
				return rb, nil
			},
		}, nil
	})
}

func unrelatedOutputBinding(id string) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[polymorphicOutput]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[polymorphicOutput](), func(ctx *workflow.Context, _ any) (any, error) {
					return nil, ctx.YieldOutput(unrelatedOutput{})
				})
				return rb, nil
			},
		}, nil
	})
}

func explicitYieldBinding(id string, output any) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[dataMessage]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, _ any) (any, error) {
					return nil, ctx.YieldOutput(output)
				})
				return rb, nil
			},
		}, nil
	})
}

func runAndCollectEvents(t *testing.T, wf *workflow.Workflow, input any) []workflow.Event {
	t.Helper()
	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer func() {
		if err := run.Close(ctx); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}()

	var events []workflow.Event
	for event := range run.OutgoingEvents() {
		events = append(events, event)
	}
	return events
}

func outputEvents(events []workflow.Event) []workflow.OutputEvent {
	var outputs []workflow.OutputEvent
	for _, event := range events {
		if output, ok := event.(workflow.OutputEvent); ok {
			outputs = append(outputs, output)
		}
	}
	return outputs
}

func errorEvents(events []workflow.Event) []workflow.ErrorEvent {
	var errors []workflow.ErrorEvent
	for _, event := range events {
		if errorEvent, ok := event.(workflow.ErrorEvent); ok {
			errors = append(errors, errorEvent)
		}
	}
	return errors
}

func TestNewExecutor_DescribesProtocol(t *testing.T) {
	id := "fn"
	binding := workflow.NewExecutor(id, func(in textMessage) dataMessage {
		return dataMessage{Bytes: []byte(in.Text)}
	}).Bind()

	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	descriptor, err := wf.DescribeProtocol()
	if err != nil {
		t.Fatalf("DescribeProtocol: %v", err)
	}
	wantIn := reflect.TypeFor[textMessage]()
	var foundIn bool
	for _, t := range descriptor.Accepts {
		if t == wantIn {
			foundIn = true
			break
		}
	}
	if !foundIn {
		t.Errorf("descriptor.Accepts = %v, want to contain %v", descriptor.Accepts, wantIn)
	}
	executor, err := binding.CreateInstance("")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	executorDescriptor := executor.DescribeProtocol()
	wantOut := reflect.TypeFor[dataMessage]()
	if !containsType(executorDescriptor.Sends, wantOut) {
		t.Errorf("executor descriptor.Sends = %v, want to contain %v", executorDescriptor.Sends, wantOut)
	}
	if !containsType(executorDescriptor.Yields, wantOut) {
		t.Errorf("executor descriptor.Yields = %v, want to contain %v", executorDescriptor.Yields, wantOut)
	}
}

func containsType(types []reflect.Type, want reflect.Type) bool {
	for _, typ := range types {
		if typ == want {
			return true
		}
	}
	return false
}

func TestFunctionExecutor_ReturnValueAutoSendAndYieldOptions(t *testing.T) {
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
			source := returnedDataBinding("source", &workflow.Executor{
				DisableAutoSendMessageHandlerResultObject: !testCase.autoSend,
				DisableAutoYieldOutputHandlerResultObject: !testCase.autoYield,
			})
			var gotAtSink []dataMessage
			sink := workflow.NewExecutor("sink", func(msg dataMessage) {
				gotAtSink = append(gotAtSink, msg)
			}).Bind()

			wf, err := workflow.NewBuilder(source).
				AddEdge(source, sink).
				WithOutputFrom(source).
				Build()
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			run, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "abc"})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			var outputs []dataMessage
			for evt := range run.OutgoingEvents() {
				if output, ok := evt.(workflow.OutputEvent); ok {
					value, ok := output.Output.(dataMessage)
					if !ok {
						t.Fatalf("OutputEvent.Output = %T, want dataMessage", output.Output)
					}
					outputs = append(outputs, value)
				}
			}

			if got := len(gotAtSink); got != boolCount(testCase.autoSend) {
				t.Fatalf("sink receive count = %d, want %d", got, boolCount(testCase.autoSend))
			}
			if got := len(outputs); got != boolCount(testCase.autoYield) {
				t.Fatalf("output count = %d, want %d", got, boolCount(testCase.autoYield))
			}
			if testCase.autoSend && string(gotAtSink[0].Bytes) != "abc" {
				t.Fatalf("sink output bytes = %q, want abc", gotAtSink[0].Bytes)
			}
			if testCase.autoYield && string(outputs[0].Bytes) != "abc" {
				t.Fatalf("yielded output bytes = %q, want abc", outputs[0].Bytes)
			}
		})
	}
}

func returnedDataBinding(id string, options *workflow.Executor) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		executor := workflow.Executor{ID: executorID}
		executor.Extend(options)
		executor.Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[textMessage](), reflect.TypeFor[dataMessage](), func(_ *workflow.Context, msg any) (any, error) {
					input := msg.(textMessage)
					return dataMessage{Bytes: []byte(input.Text)}, nil
				})
				return rb, nil
			},
		})
		return &executor, nil
	})
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestRequestPortBind_PostsRequestAndForwardsResponse(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "ask",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	portBinding := port.Bind()

	id := "sink"
	var receivedAtSink int
	var sawAtSink bool
	sinkBinding := workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[int]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[int](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					receivedAtSink = msg.(int)
					sawAtSink = true
					return nil, ctx.YieldOutput(msg)
				})
				return rb, nil
			},
		}, nil
	})

	wf, err := workflow.NewBuilder(portBinding).
		AddEdge(portBinding, sinkBinding).
		WithOutputFrom(sinkBinding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "what")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var req *workflow.ExternalRequest
	for evt := range run.OutgoingEvents() {
		if reqEvt, ok := evt.(workflow.RequestInfoEvent); ok {
			req = reqEvt.Request
			break
		}
	}
	if req == nil {
		t.Fatalf("expected a RequestInfoEvent, got none")
	}
	if req.PortInfo.PortID != port.ID {
		t.Errorf("request port ID = %q, want %q", req.PortInfo.PortID, port.ID)
	}
	if data, ok := req.Data.As(port.Request); !ok || data.(string) != "what" {
		t.Errorf("request data = %v, want %q", req.Data.Any(), "what")
	}

	resp, err := req.CreateResponse(int(42))
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	for range run.OutgoingEvents() {
	}

	if !sawAtSink {
		t.Fatalf("expected sink to receive the unwrapped response")
	}
	if receivedAtSink != 42 {
		t.Errorf("sink received = %d, want 42", receivedAtSink)
	}
}

func TestRequestPortBind_RejectsResponseForOtherPort(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "ask",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	portBinding := port.Bind()

	wf, err := workflow.NewBuilder(portBinding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "what")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var req *workflow.ExternalRequest
	for evt := range run.OutgoingEvents() {
		if reqEvt, ok := evt.(workflow.RequestInfoEvent); ok {
			req = reqEvt.Request
			break
		}
	}
	if req == nil {
		t.Fatalf("expected a RequestInfoEvent")
	}

	otherPort := workflow.RequestPort{
		ID:       "other",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	resp := &workflow.ExternalResponse{
		RequestID: req.RequestID,
		PortInfo:  workflow.NewRequestPortInfo(otherPort),
		Data:      workflow.AnyPortableValue(int(7)),
	}
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	for range run.OutgoingEvents() {
	}
}

func TestRequestPortBind_ForwardsExternalRequestAndRestoresOriginalResponse(t *testing.T) {
	outerPort := workflow.RequestPort{
		ID:       "outer",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	innerPort := workflow.RequestPort{
		ID:       "inner",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	innerBinding := innerPort.Bind()

	forwarder := workflow.BindNewExecutorFunc("forwarder", func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					request, err := workflow.NewExternalRequest("original-request", outerPort, msg)
					if err != nil {
						return nil, err
					}
					return nil, ctx.SendMessage(innerBinding.ID, request)
				})
				return rb, nil
			},
		}, nil
	})

	var gotResponse *workflow.ExternalResponse
	responseSink := workflow.NewExecutor("response-sink", func(msg *workflow.ExternalResponse) {
		gotResponse = msg
	}).Bind()

	wf, err := workflow.NewBuilder(forwarder).
		AddEdge(forwarder, innerBinding).
		AddEdge(innerBinding, responseSink).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var request *workflow.ExternalRequest
	for evt := range run.OutgoingEvents() {
		if requestEvent, ok := evt.(workflow.RequestInfoEvent); ok {
			request = requestEvent.Request
			break
		}
	}
	if request == nil {
		t.Fatal("expected forwarded RequestInfoEvent")
	}
	if request.PortInfo.PortID != innerPort.ID {
		t.Fatalf("forwarded request port = %q, want %q", request.PortInfo.PortID, innerPort.ID)
	}
	if request.RequestID != "original-request" {
		t.Fatalf("forwarded request ID = %q, want original-request", request.RequestID)
	}

	response, err := request.CreateResponse(42)
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := run.Resume(ctx, response); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	for range run.OutgoingEvents() {
	}

	if gotResponse == nil {
		t.Fatal("expected response sink to receive restored ExternalResponse")
	}
	if gotResponse.RequestID != "original-request" {
		t.Fatalf("restored response request ID = %q, want original-request", gotResponse.RequestID)
	}
	if gotResponse.PortInfo.PortID != outerPort.ID {
		t.Fatalf("restored response port = %q, want %q", gotResponse.PortInfo.PortID, outerPort.ID)
	}
	value, ok := workflow.PortableValueAs[int](gotResponse.Data)
	if !ok || value != 42 {
		t.Fatalf("restored response data = %d, %v; want 42, true", value, ok)
	}
}
