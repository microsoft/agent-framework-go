// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type (
	textMessage struct{ Text string }
	dataMessage struct{ Bytes []byte }
)

func TestBindFunc_InvokesHandler_NoOutput(t *testing.T) {
	called := false
	id := "fn"
	binding := workflow.BindFunc(id, false, func(in textMessage) struct{} {
		called = true
		if in.Text != "hello" {
			t.Errorf("handler input = %q, want %q", in.Text, "hello")
		}
		return struct{}{}
	})
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

func TestBindFunc_InvokesHandler_WithOutput(t *testing.T) {
	id := "fn"
	binding := workflow.BindFunc(id, false, func(in textMessage) dataMessage {
		return dataMessage{Bytes: []byte(in.Text)}
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

func polymorphicOutputBinding(id string, output polymorphicOutput) *workflow.ExecutorBinding {
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Options: workflow.ExecutorOptions{
				DisableAutoSendMessageHandlerResultObject: true,
			},
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[string](), reflect.TypeFor[polymorphicOutput](), false, func(_ *workflow.Context, _ any) (any, error) {
						return output, nil
					}), nil
				},
			}},
		}, nil
	}
	return binding
}

func unrelatedOutputBinding(id string) *workflow.ExecutorBinding {
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
					return rb.AddHandler(reflect.TypeFor[string](), reflect.TypeFor[polymorphicOutput](), false, func(ctx *workflow.Context, _ any) (any, error) {
						return nil, ctx.YieldOutput(unrelatedOutput{})
					}), nil
				},
			}},
		}, nil
	}
	return binding
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

func TestBindFunc_DescribesProtocol(t *testing.T) {
	id := "fn"
	binding := workflow.BindFunc(id, false, func(in textMessage) dataMessage {
		return dataMessage{Bytes: []byte(in.Text)}
	})
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
}

func TestBindFuncContext_InvokesHandlerWithContext(t *testing.T) {
	id := "fn"
	got := make(chan context.Context, 1)
	binding := workflow.BindFuncContext(id, false, func(ctx context.Context, in textMessage) struct{} {
		got <- ctx
		return struct{}{}
	})
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := inproc.Default.Run(context.Background(), wf, textMessage{Text: "hello"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	select {
	case ctx := <-got:
		if ctx == nil {
			t.Fatal("handler received nil context")
		}
	default:
		t.Fatal("handler did not receive a context")
	}
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
			source := returnedDataBinding("source", workflow.ExecutorOptions{
				DisableAutoSendMessageHandlerResultObject: !testCase.autoSend,
				DisableAutoYieldOutputHandlerResultObject: !testCase.autoYield,
			})
			sink := &workflow.ExecutorBinding{
				ID:           "sink",
				ExecutorType: reflect.TypeFor[*workflow.Executor](),
			}
			var gotAtSink []dataMessage
			sink.NewExecutor = func(_ string) (*workflow.Executor, error) {
				return &workflow.Executor{
					ID: sink.ID,
					Options: workflow.ExecutorOptions{
						DisableAutoSendMessageHandlerResultObject: true,
						DisableAutoYieldOutputHandlerResultObject: true,
					},
					Config: []*workflow.ExecutorConfig{{
						ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
							return rb.AddHandler(reflect.TypeFor[dataMessage](), nil, false, func(_ *workflow.Context, msg any) (any, error) {
								gotAtSink = append(gotAtSink, msg.(dataMessage))
								return nil, nil
							}), nil
						},
					}},
				}, nil
			}

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

func returnedDataBinding(id string, options workflow.ExecutorOptions) *workflow.ExecutorBinding {
	binding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	binding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID:      id,
			Options: options,
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[textMessage](), reflect.TypeFor[dataMessage](), false, func(_ *workflow.Context, msg any) (any, error) {
						input := msg.(textMessage)
						return dataMessage{Bytes: []byte(input.Text)}, nil
					}), nil
				},
			}},
		}, nil
	}
	return binding
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestBindRequestPort_PostsRequestAndForwardsResponse(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "ask",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	portBinding := workflow.BindRequestPort(port)

	id := "sink"
	sinkBinding := &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	var receivedAtSink int
	var sawAtSink bool
	sinkBinding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,
			Options: workflow.ExecutorOptions{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
			},
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[int](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
						receivedAtSink = msg.(int)
						sawAtSink = true
						return nil, ctx.YieldOutput(msg)
					}), nil
				},
			}},
		}, nil
	}

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

	resp, err := req.NewResponse(int(42))
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
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

func TestBindRequestPort_RejectsResponseForOtherPort(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "ask",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	portBinding := workflow.BindRequestPort(port)

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
		RequestID: req.ID,
		PortInfo:  workflow.NewRequestPortInfo(otherPort),
		Data:      workflow.AnyPortableValue(int(7)),
	}
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	for range run.OutgoingEvents() {
	}
}

func TestBindRequestPort_ForwardsExternalRequestAndRestoresOriginalResponse(t *testing.T) {
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
	innerBinding := workflow.BindRequestPort(innerPort)

	forwarder := &workflow.ExecutorBinding{
		ID:           "forwarder",
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	forwarder.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: forwarder.ID,
			Options: workflow.ExecutorOptions{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
			},
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[string](), nil, false, func(ctx *workflow.Context, msg any) (any, error) {
						request, err := workflow.NewExternalRequest("original-request", outerPort, msg)
						if err != nil {
							return nil, err
						}
						return nil, ctx.SendMessage(innerBinding.ID, request)
					}), nil
				},
			}},
		}, nil
	}

	responseSink := &workflow.ExecutorBinding{
		ID:           "response-sink",
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	var gotResponse *workflow.ExternalResponse
	responseSink.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: responseSink.ID,
			Options: workflow.ExecutorOptions{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
			},
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[*workflow.ExternalResponse](), nil, false, func(_ *workflow.Context, msg any) (any, error) {
						gotResponse = msg.(*workflow.ExternalResponse)
						return nil, nil
					}), nil
				},
			}},
		}, nil
	}

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
	if request.ID != "original-request" {
		t.Fatalf("forwarded request ID = %q, want original-request", request.ID)
	}

	response, err := request.NewResponse(42)
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
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
