// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"reflect"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Checkpoint and Resume Workflow",
	"This sample resumes a workflow from a checkpoint after an external request.",
)

func main() {
	approvalPort := workflow.RequestPort{
		ID:       "ApprovalPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[string](),
	}
	approval := workflow.BindRequestPort(approvalPort)
	finalize := workflow.BindFunc("FinalizeExecutor", func(response string) string {
		return "Human response after resume: " + response
	})

	wf, err := workflow.NewBuilder(approval).
		AddEdge(approval, finalize).
		WithOutputFrom(finalize).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()

	manager := checkpoint.NewInMemoryManager()
	first, err := inproc.Default.WithCheckpointing(manager).Run(ctx, wf, "Need deployment approval")
	if err != nil {
		demo.Panic(err)
	}

	checkpointInfo, ok := first.LastCheckpoint()
	if !ok {
		demo.Panic("expected checkpoint")
	}
	request := firstRequest(first.OutgoingEvents())
	if request == nil {
		demo.Panic("expected request")
	}
	if err := first.Close(ctx); err != nil {
		demo.Panic(err)
	}

	resumed, err := inproc.Default.WithCheckpointing(manager).Resume(ctx, wf, checkpointInfo)
	if err != nil {
		demo.Panic(err)
	}
	demo.Assistantf("Resumed from checkpoint %s", checkpointInfo.CheckpointID)

	response, err := request.CreateResponse("approved")
	if err != nil {
		demo.Panic(err)
	}
	if _, err := resumed.Resume(ctx, response); err != nil {
		demo.Panic(err)
	}
	for evt := range resumed.NewEvents() {
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}

func firstRequest(events func(func(workflow.Event) bool)) *workflow.ExternalRequest {
	for evt := range events {
		if req, ok := evt.(workflow.RequestInfoEvent); ok {
			return req.Request
		}
	}
	return nil
}
