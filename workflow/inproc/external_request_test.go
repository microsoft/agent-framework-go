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
					return rb.
						AddHandler(reflect.TypeFor[string](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
							req, err := workflow.NewExternalRequest("req-1", port, "what is your name?")
							if err != nil {
								return nil, err
							}
							return nil, wctx.PostRequest(req)
						}).
						AddHandler(reflect.TypeFor[*workflow.ExternalResponse](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
							resp := msg.(*workflow.ExternalResponse)
							data, _ := resp.Data.As(port.Response)
							return nil, wctx.YieldOutput(data)
						}), nil
				},
			}},
		}, nil
	}

	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	run, err := inproc.Run(ctx, wf, "", "kick")
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
	resp, err := req.NewResponse("Alice")
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
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
	startBinding := &workflow.ExecutorBinding{
		ID:           startID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	startBinding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: startID,
			Options: workflow.ExecutorOptions{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
			},
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandler(reflect.TypeFor[string](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
						return nil, wctx.SendMessage("", "go")
					}), nil
				},
			}},
		}, nil
	}

	// Asker node: a separate, non-start executor that posts and handles the response.
	gotResponseAtAsker := false
	askerID := "asker"
	askerBinding := &workflow.ExecutorBinding{
		ID:           askerID,
		ExecutorType: reflect.TypeFor[*workflow.Executor](),
	}
	askerBinding.NewExecutor = func(_ string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: askerID,
			Options: workflow.ExecutorOptions{
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
			},
			Config: []*workflow.ExecutorConfig{{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.
						AddHandler(reflect.TypeFor[string](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
							req, err := workflow.NewExternalRequest("req-2", port, "ping")
							if err != nil {
								return nil, err
							}
							return nil, wctx.PostRequest(req)
						}).
						AddHandler(reflect.TypeFor[*workflow.ExternalResponse](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
							gotResponseAtAsker = true
							return nil, wctx.YieldOutput("done")
						}), nil
				},
			}},
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
	run, err := inproc.Run(ctx, wf, "", "kick")
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
	resp, _ := req.NewResponse("pong")
	if _, err := run.Resume(ctx, resp); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if !gotResponseAtAsker {
		t.Errorf("expected response routed to asker, got none")
	}
}

func TestExternalResponse_UnsolicitedResponseErrors(t *testing.T) {
	id := "noop"
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
					return rb.AddHandler(reflect.TypeFor[string](), nil, false, func(_ *workflow.Context, _ any) (any, error) {
						return nil, nil
					}), nil
				},
			}},
		}, nil
	}
	wf, err := workflow.NewBuilder(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx := context.Background()
	stream, err := inproc.OpenStream(ctx, wf, "")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer stream.Cancel()

	port := workflow.RequestPort{
		ID:       "p",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	fakeResp := &workflow.ExternalResponse{
		RequestID:   "no-such-id",
		RequestPort: port,
		Data:        workflow.AnyPortableValue(int(7)),
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
	if _, err := req.NewResponse(int(42)); err != nil {
		t.Errorf("matching response type should succeed: %v", err)
	}
	if _, err := req.NewResponse("wrong"); err == nil {
		t.Error("non-matching response type should fail")
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
	if r1.ID == "" || r2.ID == "" {
		t.Fatalf("expected non-empty IDs, got %q, %q", r1.ID, r2.ID)
	}
	if r1.ID == r2.ID {
		t.Errorf("expected unique IDs, got %q twice", r1.ID)
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
	if r.ID != "user-supplied" {
		t.Errorf("ID = %q, want %q", r.ID, "user-supplied")
	}
}
