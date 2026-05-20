// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Concurrent Workflow",
	"This sample fans a question out to multiple executors and aggregates their answers.",
)

func main() {
	start := bindStep[string]("ConcurrentStartExecutor", func(ctx *workflow.Context, question string) error {
		return ctx.SendMessage("", question)
	})
	physics := workflow.BindFunc("Physicist", func(question string) string {
		return fmt.Sprintf("physics: %s relates to average kinetic energy", strings.TrimSuffix(question, "?"))
	})
	chemistry := workflow.BindFunc("Chemist", func(question string) string {
		return fmt.Sprintf("chemistry: %s affects reaction rates and phase changes", strings.TrimSuffix(question, "?"))
	})
	aggregate := aggregateStrings("ConcurrentAggregationExecutor")

	wf, err := workflow.NewBuilder(start).
		AddFanOutEdge(start, []workflow.ExecutorBinding{physics, chemistry}).
		AddFanInBarrierEdge([]workflow.ExecutorBinding{physics, chemistry}, aggregate).
		WithOutputFrom(aggregate).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Concurrent.RunStreaming(context.Background(), wf, "What is temperature?")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(context.Background()) }()

	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistantf("Workflow completed with results:\n%v", output.Output)
		}
	}
}

func bindStep[In any](id string, fn func(*workflow.Context, In) error) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeOf(fn),
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return &workflow.Executor{
				ID: id,
				Spec: workflow.ExecutorSpec{
					DisableAutoSendMessageHandlerResultObject: true,
					DisableAutoYieldOutputHandlerResultObject: true,
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[In](), nil, func(ctx *workflow.Context, msg any) (any, error) {
							return struct{}{}, fn(ctx, msg.(In))
						})
						return rb, nil
					},
				},
			}, nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}

func aggregateStrings(id string) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[string](),
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			var messages []string
			return &workflow.Executor{
				ID: id,
				Spec: workflow.ExecutorSpec{
					DisableAutoSendMessageHandlerResultObject: true,
					DisableAutoYieldOutputHandlerResultObject: true,
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(_ *workflow.Context, msg any) (any, error) {
							messages = append(messages, msg.(string))
							return struct{}{}, nil
						})
						return rb, nil
					},
					OnMessageDeliveryFinished: func(ctx *workflow.Context) error {
						if len(messages) == 0 {
							return nil
						}
						out := strings.Join(messages, "\n") + "\n"
						messages = nil
						return ctx.YieldOutput(out)
					},
				},
			}, nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}
