// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type textMessage struct{ Text string }
type dataMessage struct{ Bytes []byte }

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

	if _, err := inproc.Run(context.Background(), wf, "", textMessage{Text: "hello"}); err != nil {
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

	run, err := inproc.Run(context.Background(), wf, "", textMessage{Text: "abc"})
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

	if _, err := inproc.Run(context.Background(), wf, "", textMessage{Text: "hello"}); err != nil {
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
	run, err := inproc.Run(ctx, wf, "", "what")
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
	if req.RequestPort.ID != port.ID {
		t.Errorf("request port ID = %q, want %q", req.RequestPort.ID, port.ID)
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
	run, err := inproc.Run(ctx, wf, "", "what")
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
		RequestID:   req.ID,
		RequestPort: otherPort,
		Data:        workflow.AnyPortableValue(int(7)),
	}
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	for range run.OutgoingEvents() {
	}
}
