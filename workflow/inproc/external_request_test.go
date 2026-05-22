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

type requestPortStringer string

func (s requestPortStringer) String() string { return string(s) }

type requestNilPayload struct{}

// TestPostRequestFromExecutor verifies that an executor can raise an
// ExternalRequest via Context.PostRequest, that the matching ExternalResponse
// is routed back to that executor (not to the start executor), and that the
// executor's response handler runs.
func TestPostRequestFromExecutor(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "ask-user",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}

	// An executor that:
	//   * on string input: posts an ExternalRequest, then halts.
	//   * on *ExternalResponse: yields the response data as workflow output.
	id := "asker"
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
					rb.YieldsOutputType(reflect.TypeFor[string]())
					rb.RouteBuilder.
						AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, msg any) (any, error) {
							req, err := workflow.NewExternalRequest("req-1", port, "what is your name?")
							if err != nil {
								return nil, err
							}
							return nil, wctx.PostRequest(req)
						}).
						AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(wctx *workflow.Context, msg any) (any, error) {
							resp := msg.(*workflow.ExternalResponse)
							data, _ := resp.Data.As(port.Response)
							return nil, wctx.YieldOutput(data)
						})
					return rb, nil
				},
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "kick")
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
		t.Fatalf("expected RequestInfoEvent, got none")
	}
	resp, err := req.CreateResponse("Alice")
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	var gotOutput any
	for evt := range run.OutgoingEvents() {
		if outEvt, ok := evt.(workflow.OutputEvent); ok {
			gotOutput = outEvt.Output
		}
	}
	if got, want := gotOutput, any("Alice"); got != want {
		t.Errorf("output = %v, want %v", got, want)
	}
}

// TestPostRequestRoutingToOwner verifies that when a non-start executor posts
// a request, the response goes to that executor (not the start executor).
func TestPostRequestRoutingToOwner(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}

	// Start node: forwards an int to the asker.
	startID := "start"
	startBinding := workflow.ExecutorBinding{
		ID:           startID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	startBinding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: startID,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.SendsMessageType(reflect.TypeFor[string]())
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, msg any) (any, error) {
						return nil, wctx.SendMessage("", "go")
					})
					return rb, nil
				},
			},
		}, nil
	}

	// Asker node: a separate, non-start executor that posts and handles the response.
	gotResponseAtAsker := false
	askerID := "asker"
	askerBinding := workflow.ExecutorBinding{
		ID:           askerID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	askerBinding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: askerID,
			Spec: workflow.ExecutorSpec{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.YieldsOutputType(reflect.TypeFor[string]())
					rb.RouteBuilder.
						AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, msg any) (any, error) {
							req, err := workflow.NewExternalRequest("req-2", port, "ping")
							if err != nil {
								return nil, err
							}
							return nil, wctx.PostRequest(req)
						}).
						AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(wctx *workflow.Context, msg any) (any, error) {
							gotResponseAtAsker = true
							return nil, wctx.YieldOutput("done")
						})
					return rb, nil
				},
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(startBinding).
		AddEdge(startBinding, askerBinding).
		WithOutputFrom(askerBinding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "kick")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Find the request and respond.
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
	resp, _ := req.CreateResponse("pong")
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if !gotResponseAtAsker {
		t.Errorf("expected response routed to asker, got none")
	}
}

func TestExternalResponse_UnsolicitedResponseErrors(t *testing.T) {
	id := "noop"
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
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(_ *workflow.Context, _ any) (any, error) {
						return nil, nil
					})
					return rb, nil
				},
			},
		}, nil
	}
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

	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	fakeResp := &workflow.ExternalResponse{
		RequestID: "no-such-id",
		PortInfo:  workflow.NewRequestPortInfo(port),
		Data:      workflow.AnyPortableValue(int(7)),
	}
	if err := stream.SendResponse(ctx, fakeResp); err != nil {
		t.Fatalf("SendResponse: %v", err)
	}

	var sawErr bool
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		if e, ok := evt.(workflow.ErrorEvent); ok && e.Error != nil &&
			strings.Contains(e.Error.Error(), "no pending request") {
			sawErr = true
			break
		}
	}
	if !sawErr {
		t.Errorf("expected an ErrorEvent referencing 'no pending request', got none")
	}
}

func TestExternalRequest_NewRequest_TypeValidation(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	if _, err := workflow.NewExternalRequest("", port, "ok"); err != nil {
		t.Errorf("matching type should succeed: %v", err)
	}
	if _, err := workflow.NewExternalRequest("", port, 42); err == nil {
		t.Error("non-matching type should fail")
	}
}

func TestExternalRequest_NewRequest_RejectsNil(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[*requestNilPayload](),
		Response: reflect.TypeFor[int](),
	}
	if _, err := workflow.NewExternalRequest("", port, nil); err == nil {
		t.Fatal("nil request data should fail")
	}
	var typedNil *requestNilPayload
	if _, err := workflow.NewExternalRequest("", port, typedNil); err == nil {
		t.Fatal("typed nil request data should fail")
	}
}

func TestExternalRequest_NewRequest_AssignableTypeValidation(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[interface{ String() string }](),
		Response: reflect.TypeFor[int](),
	}
	if _, err := workflow.NewExternalRequest("", port, requestPortStringer("ok")); err != nil {
		t.Errorf("assignable request type should succeed: %v", err)
	}
	if _, err := workflow.NewExternalRequest("", port, 42); err == nil {
		t.Error("non-assignable request type should fail")
	}
}

func TestExternalRequest_NewRequest_PortableValuePointerIsValidatedAsWrapper(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	pv := workflow.AnyPortableValue("ok")
	if _, err := workflow.NewExternalRequest("", port, &pv); err == nil {
		t.Fatal("NewExternalRequest should validate *PortableValue as its concrete wrapper type")
	}
}

func TestExternalRequest_NewResponse_TypeValidation(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	req, err := workflow.NewExternalRequest("rid", port, "hi")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}
	if _, err := req.CreateResponse(int(42)); err != nil {
		t.Errorf("matching response type should succeed: %v", err)
	}
	if _, err := req.CreateResponse("wrong"); err == nil {
		t.Error("non-matching response type should fail")
	}
}

func TestExternalRequest_NewResponse_RejectsNil(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[*requestNilPayload](),
	}
	req, err := workflow.NewExternalRequest("rid", port, "hi")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}
	if _, err := req.CreateResponse(nil); err == nil {
		t.Fatal("nil response data should fail")
	}
	var typedNil *requestNilPayload
	if _, err := req.CreateResponse(typedNil); err == nil {
		t.Fatal("typed nil response data should fail")
	}
}

func TestExternalRequest_UsesPortInfo(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	req, err := workflow.NewExternalRequest("rid", port, "hi")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}
	if got, want := req.PortInfo, workflow.NewRequestPortInfo(port); got != want {
		t.Fatalf("PortInfo = %#v, want %#v", got, want)
	}
	resp, err := req.CreateResponse(int(42))
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if got, want := resp.PortInfo, req.PortInfo; got != want {
		t.Errorf("response PortInfo = %#v, want %#v", got, want)
	}
}

func TestExternalRequest_AssignsRandomIDWhenEmpty(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	r1, err := workflow.NewExternalRequest("", port, "a")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}
	r2, err := workflow.NewExternalRequest("", port, "a")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}
	if r1.RequestID == "" || r2.RequestID == "" {
		t.Fatalf("expected non-empty IDs, got %q, %q", r1.RequestID, r2.RequestID)
	}
	if r1.RequestID == r2.RequestID {
		t.Errorf("expected unique IDs, got %q twice", r1.RequestID)
	}
}

func TestExternalRequest_PreservesProvidedID(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	r, err := workflow.NewExternalRequest("user-supplied", port, "a")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}
	if r.RequestID != "user-supplied" {
		t.Errorf("ID = %q, want %q", r.RequestID, "user-supplied")
	}
}
