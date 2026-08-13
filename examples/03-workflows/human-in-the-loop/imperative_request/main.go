// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"reflect"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Imperative Human in the Loop Workflow",
	"This sample raises a human-in-the-loop request imperatively from inside an executor via Context.PostRequest, the low-level counterpart to the request-port binding used by human_in_the_loop_basic.",
)

// askPort describes the human-in-the-loop exchange: the executor asks a
// question (a string) and expects an answer (a string) back.
var askPort = workflow.RequestPort{
	ID:       "AskUser",
	Request:  reflect.TypeFor[string](),
	Response: reflect.TypeFor[string](),
}

func main() {
	// Instead of binding askPort into the graph (as human_in_the_loop_basic
	// does), a single custom executor raises the request imperatively and
	// consumes the matching response itself.
	binding := workflow.ExecutorBinding{
		ID:               "greeter",
		ImplementationID: "*workflow.Executor",
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: "greeter",

				// The handlers drive the request/response protocol directly, so
				// disable the automatic message/output plumbing that the
				// convenience executors rely on.
				DisableAutoSendMessageHandlerResultObject: true,
				DisableAutoYieldOutputHandlerResultObject: true,
				ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
					rb.YieldsOutputType(reflect.TypeFor[string]())
					rb.RouteBuilder.
						// On the initial string input, raise an ExternalRequest
						// and halt until it is answered.
						AddHandlerRaw(reflect.TypeFor[string](), nil, func(wctx *workflow.Context, _ any) (any, error) {
							req, err := workflow.NewExternalRequest("greeting-1", askPort, "What is your name?")
							if err != nil {
								return nil, err
							}
							return nil, wctx.PostRequest(req)
						}).
						// The matching ExternalResponse comes back as a regular
						// message; read its data and yield the greeting.
						AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(wctx *workflow.Context, msg any) (any, error) {
							resp := msg.(*workflow.ExternalResponse)
							data, ok := resp.Data.As(askPort.Response)
							if !ok {
								return nil, nil
							}
							return nil, wctx.YieldOutput("Hello, " + data.(string) + "!")
						})
					return rb, nil
				},
			}, nil
		},
	}

	wf, err := workflow.NewBuilder(binding).
		WithOutputFrom(binding).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()
	run, err := inproc.Default.Run(ctx, wf, "start")
	if err != nil {
		demo.Panic(err)
	}

	// First pass: drain events until the executor raises its request.
	var req *workflow.ExternalRequest
	for evt := range run.OutgoingEvents() {
		switch e := evt.(type) {
		case workflow.RequestInfoEvent:
			req = e.Request
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		}
	}
	if req == nil {
		demo.Panic("workflow completed without raising a request")
	}

	prompt, _ := req.Data.As(askPort.Request)
	demo.Assistantf("Executor asked: %v", prompt)

	// A real application would collect this from a person; here we answer with
	// a fixed value to keep the sample deterministic.
	const answer = "Ada"
	demo.Assistantf("Answering with: %s", answer)
	response, err := req.CreateResponse(answer)
	if err != nil {
		demo.Panic(err)
	}

	// Resume the run with the response and drain the resulting output.
	if _, err := run.Resume(ctx, response); err != nil {
		demo.Panic(err)
	}
	for evt := range run.OutgoingEvents() {
		switch e := evt.(type) {
		case workflow.OutputEvent:
			demo.Assistantf("Workflow completed with result: %v", e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		}
	}
}
