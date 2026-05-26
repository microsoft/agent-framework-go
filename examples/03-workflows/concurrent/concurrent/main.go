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
	start := workflow.NewExecutor("ConcurrentStartExecutor", func(ctx *workflow.Context, question string) error {
		return ctx.SendMessage("", question)
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[string]())
			return rb, nil
		},
	}).Bind()

	physics := workflow.NewExecutor("Physicist", func(question string) string {
		return fmt.Sprintf("physics: %s relates to average kinetic energy", strings.TrimSuffix(question, "?"))
	}).Bind()

	chemistry := workflow.NewExecutor("Chemist", func(question string) string {
		return fmt.Sprintf("chemistry: %s affects reaction rates and phase changes", strings.TrimSuffix(question, "?"))
	}).Bind()

	aggregate := aggregateStrings("ConcurrentAggregationExecutor")

	wf, err := workflow.NewBuilder(start).
		AddFanOutEdge(start, []workflow.ExecutorBinding{physics, chemistry}).
		AddFanInBarrierEdge([]workflow.ExecutorBinding{physics, chemistry}, aggregate).
		WithOutputFrom(aggregate).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.RunStreaming(context.Background(), wf, "What is temperature?")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(context.Background()) }()

	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			output := e
			demo.Assistantf("Workflow completed with results:\n%v", output.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
}

func aggregateStrings(id string) workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		var messages []string
		return workflow.NewExecutor(executorID, func(msg string) {
			messages = append(messages, msg)
		}).Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[string]())
				return rb, nil
			},
			OnMessageDeliveryFinishedFunc: func(ctx *workflow.Context) error {
				if len(messages) == 0 {
					return nil
				}
				out := strings.Join(messages, "\n") + "\n"
				messages = nil
				return ctx.YieldOutput(out)
			},
		}), nil
	})
}
