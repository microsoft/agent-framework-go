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
	"Human in the Loop Workflow",
	"This sample pauses a workflow for an external human approval response.",
)

func main() {
	approvalPort := workflow.RequestPort{
		ID:       "ApprovalPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[bool](),
	}
	approval := workflow.BindRequestPort(approvalPort)
	finalize := workflow.BindFunc("FinalizeExecutor", func(approved bool) string {
		if approved {
			return "Request approved by the human reviewer"
		}
		return "Request rejected by the human reviewer"
	})

	wf, err := workflow.NewBuilder(approval).
		AddEdge(approval, finalize).
		WithOutputFrom(finalize).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, "Approve deployment to production?")
	if err != nil {
		demo.Panic(err)
	}

	var request *workflow.ExternalRequest
	for evt := range run.NewEvents() {
		if req, ok := evt.(workflow.RequestInfoEvent); ok {
			request = req.Request
			demo.Assistantf("Human input requested: %v", request.Data.Any())
		}
	}
	if request == nil {
		demo.Panic("expected approval request")
	}

	response, err := request.NewResponse(true)
	if err != nil {
		demo.Panic(err)
	}
	if _, err := run.Resume(context.Background(), response); err != nil {
		demo.Panic(err)
	}
	for evt := range run.NewEvents() {
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}
