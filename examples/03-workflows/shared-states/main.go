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
	"Shared State Workflow",
	"This sample writes and reads shared workflow state between executors.",
)

const stateScope = "EmailState"

type EmailRecord struct {
	ID      string
	Subject string
	Body    string
}

type EmailDecision struct {
	EmailID string
	Spam    bool
	Reason  string
}

func main() {
	ingest := bindContextFunc("EmailIngestExecutor", func(ctx *workflow.Context, email string) (EmailDecision, error) {
		record := EmailRecord{ID: "email-001", Subject: "Planning", Body: email}
		if err := ctx.QueueStateUpdate(record.ID, stateScope, record); err != nil {
			return EmailDecision{}, err
		}
		return EmailDecision{EmailID: record.ID, Spam: false, Reason: "known sender"}, nil
	})
	responder := bindContextFunc("EmailResponderExecutor", func(ctx *workflow.Context, decision EmailDecision) (string, error) {
		value, err := ctx.ReadState(decision.EmailID, stateScope)
		if err != nil {
			return "", err
		}
		record := value.(EmailRecord)
		return "Draft response to " + record.Subject + ": thanks for the update", nil
	})

	wf, err := workflow.NewBuilder(ingest).
		AddEdge(ingest, responder).
		WithOutputFrom(responder).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, "Can we move the planning meeting to Friday?")
	if err != nil {
		demo.Panic(err)
	}
	for evt := range run.NewEvents() {
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}

func bindContextFunc[In, Out any](id string, fn func(*workflow.Context, In) (Out, error)) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeOf(fn),
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{ID: id, Spec: workflow.ExecutorSpec{
				ConfigureRoutes: func(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
					return rb.AddHandlerRaw(reflect.TypeFor[In](), reflect.TypeFor[Out](), func(ctx *workflow.Context, msg any) (any, error) {
						return fn(ctx, msg.(In))
					}), nil
				},
			}}, nil
		},
	}
}
