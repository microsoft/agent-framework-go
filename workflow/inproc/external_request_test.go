// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type requestPortStringer string

func (s requestPortStringer) String() string { return string(s) }

type requestNilPayload struct{}

func TestPostRequest_NilRejected(t *testing.T) {
	binding := workflow.ExecutorBinding{
		ID:               "poster",
		ImplementationID: "*workflow.Executor",
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "poster",

				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *workflow.Context, _ any) (any, error) {
						return nil, ctx.PostRequest(nil)
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
	run, err := inproc.Default.Run(t.Context(), wf, "start")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var gotErr error
	for evt := range run.OutgoingEvents() {
		if errEvt, ok := evt.(workflow.ErrorEvent); ok {
			gotErr = errEvt.Error
			break
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "request cannot be nil") {
		t.Fatalf("PostRequest(nil) error = %v, want request cannot be nil", gotErr)
	}
}

func TestExternalResponse_NilRejectedBeforeRunAdvance(t *testing.T) {
	binding := workflow.NewExecutor("echo", func(message string) string { return message }).Bind()
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	run, err := inproc.Default.Run(t.Context(), wf, "start")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := run.Resume(t.Context(), (*workflow.ExternalResponse)(nil)); err == nil || !strings.Contains(err.Error(), "response cannot be nil") {
		t.Fatalf("Resume(nil ExternalResponse) error = %v, want response cannot be nil", err)
	}
}

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
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,

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
		ID:               startID,
		ImplementationID: "*workflow.Executor",
	}
	startBinding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: startID,

			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, msg any) (any, error) {
					return nil, wctx.SendMessage("", "go")
				})
				return rb, nil
			},
		}, nil
	}

	// Asker node: a separate, non-start executor that posts and handles the response.
	gotResponseAtAsker := false
	askerID := "asker"
	askerBinding := workflow.ExecutorBinding{
		ID:               askerID,
		ImplementationID: "*workflow.Executor",
	}
	askerBinding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: askerID,

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
		ID:               id,
		ImplementationID: "*workflow.Executor",
	}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,

			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(_ *workflow.Context, _ any) (any, error) {
					return nil, nil
				})
				return rb, nil
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

func TestExternalResponse_RejectsForgedPortIDWithoutConsumingRequest(t *testing.T) {
	portA := workflow.RequestPort{ID: "portA", Request: reflect.TypeFor[string](), Response: reflect.TypeFor[int]()}
	portB := workflow.RequestPort{ID: "portB", Request: reflect.TypeFor[string](), Response: reflect.TypeFor[int]()}

	id := "asker"
	binding := workflow.ExecutorBinding{ID: id, ImplementationID: "*workflow.Executor"}
	binding.NewExecutorFunc = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: id,

			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[int]())
				rb.RouteBuilder.
					AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, _ any) (any, error) {
						req, err := workflow.NewExternalRequest("request-1", portA, "data")
						if err != nil {
							return nil, err
						}
						return nil, wctx.PostRequest(req)
					}).
					AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(wctx *workflow.Context, msg any) (any, error) {
						resp := msg.(*workflow.ExternalResponse)
						data, _ := resp.Data.As(portA.Response)
						return nil, wctx.YieldOutput(data)
					})
				return rb, nil
			},
		}, nil
	}

	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Lockstep.Run(ctx, wf, "kick")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var pending *workflow.ExternalRequest
	for evt := range run.OutgoingEvents() {
		if requestInfo, ok := evt.(workflow.RequestInfoEvent); ok {
			pending = requestInfo.Request
			break
		}
	}
	if pending == nil {
		t.Fatal("expected pending external request")
	}

	forged := &workflow.ExternalResponse{
		PortInfo:  workflow.NewRequestPortInfo(portB),
		RequestID: pending.RequestID,
		Data:      workflow.AnyPortableValue(42),
	}
	if _, err := run.Resume(ctx, forged); err == nil || !strings.Contains(err.Error(), "response port id") {
		t.Fatalf("forged response error = %v, want port mismatch", err)
	}

	legitimate, err := pending.CreateResponse(42)
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := run.Resume(ctx, legitimate); err != nil {
		t.Fatalf("legitimate response after rejected forged response: %v", err)
	}

	var gotOutput any
	for evt := range run.NewEvents() {
		if out, ok := evt.(workflow.OutputEvent); ok {
			gotOutput = out.Output
		}
	}
	if gotOutput != 42 {
		t.Fatalf("output = %v, want 42", gotOutput)
	}
}

// TestCheckpoint_RequestPortRestoresWrappedRequests verifies that a request-port
// executor which re-wraps an inbound *ExternalRequest (the subworkflow-style
// forwarding path) persists its wrapped-request map across checkpoint/restore.
// After resuming in a fresh runner, the matching response must be re-wrapped
// with the ORIGINAL request PortInfo and forwarded as a single *ExternalResponse
// — not forwarded raw as the port response plus the decoded payload.
func TestCheckpoint_RequestPortRestoresWrappedRequests(t *testing.T) {
	ctx := context.Background()

	outerPort := workflow.RequestPort{
		ID:       "OuterPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}
	innerPort := workflow.RequestPort{
		ID:       "InnerPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}
	portBinding := outerPort.Bind()

	// Source executor forwards an *ExternalRequest (carrying the inner port's
	// PortInfo) into the request-port executor, exercising the re-wrapping path.
	sourceID := "Source"
	sourceBinding := workflow.ExecutorBinding{
		ID:               sourceID,
		ImplementationID: "*workflow.Executor",
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: sourceID,
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.SendsMessageType(reflect.TypeFor[*workflow.ExternalRequest]())
					rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, _ any) (any, error) {
						req, err := workflow.NewExternalRequest("wrapped-req", innerPort, "question")
						if err != nil {
							return nil, err
						}
						return nil, wctx.SendMessage("", req)
					})
					return rb, nil
				},
			}, nil
		},
	}

	// Sink records every message the request-port executor forwards to it.
	var mu sync.Mutex
	var received []any
	sinkID := "Sink"
	sinkBinding := workflow.ExecutorBinding{
		ID:               sinkID,
		ImplementationID: "*workflow.Executor",
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			record := func(msg any) (any, error) {
				mu.Lock()
				received = append(received, msg)
				mu.Unlock()
				return nil, nil
			}
			return &workflow.Executor{
				ID: sinkID,
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.RouteBuilder.
						AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(_ *workflow.Context, msg any) (any, error) {
							return record(msg)
						}).
						AddHandlerRaw(reflect.TypeFor[string](), nil, func(_ *workflow.Context, msg any) (any, error) {
							return record(msg)
						})
					return rb, nil
				},
			}, nil
		},
	}

	wf, err := workflow.NewBuilder(sourceBinding).
		AddEdge(sourceBinding, portBinding).
		AddEdge(portBinding, sinkBinding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	manager := checkpoint.NewInMemoryManager()
	first, err := inproc.Default.WithCheckpointing(manager).Run(ctx, wf, "kick")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	pendingRequest := firstRequest(t, first.OutgoingEvents())
	if pendingRequest.PortInfo.PortID != outerPort.ID {
		t.Fatalf("pending request port = %q, want %q", pendingRequest.PortInfo.PortID, outerPort.ID)
	}
	checkpointInfo, ok := first.LastCheckpoint()
	if !ok {
		t.Fatal("expected checkpoint")
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("Close first run: %v", err)
	}

	// Resume in a fresh runner: the request-port executor's in-memory wrapped
	// map starts empty and must be repopulated from the restored state.
	resumed, err := inproc.Default.WithCheckpointing(manager).Resume(ctx, wf, checkpointInfo)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	replayed := firstRequest(t, resumed.NewEvents())
	if replayed.RequestID != pendingRequest.RequestID {
		t.Fatalf("replayed request ID = %q, want %q", replayed.RequestID, pendingRequest.RequestID)
	}

	response, err := replayed.CreateResponse("answer")
	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if _, err := resumed.Resume(ctx, response); err != nil {
		t.Fatalf("Resume with response: %v", err)
	}
	for range resumed.NewEvents() {
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("sink received %d messages %#v, want 1 re-wrapped *ExternalResponse", len(received), received)
	}
	resp, ok := received[0].(*workflow.ExternalResponse)
	if !ok {
		t.Fatalf("sink message type = %T, want *workflow.ExternalResponse", received[0])
	}
	if resp.PortInfo.PortID != innerPort.ID {
		t.Fatalf("re-wrapped response port = %q, want original %q", resp.PortInfo.PortID, innerPort.ID)
	}
	data, dataOK := resp.Data.As(innerPort.Response)
	if !dataOK || data != "answer" {
		t.Fatalf("re-wrapped response data = %v (ok=%v), want %q", data, dataOK, "answer")
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
