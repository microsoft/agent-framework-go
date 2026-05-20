// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Loop Workflow",
	"This sample loops through executors until a number guessing workflow converges.",
)

type NumberSignal int

const (
	Init NumberSignal = iota
	Above
	Below
)

func main() {
	guess := newGuessNumberExecutor("GuessNumber", 1, 100)
	judge := newJudgeExecutor("Judge", 42)

	wf, err := workflow.NewBuilder(guess).
		AddEdge(guess, judge).
		AddEdge(judge, guess).
		WithOutputFrom(judge).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.RunStreaming(context.Background(), wf, Init)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(context.Background()) }()

	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}

func newGuessNumberExecutor(id string, low int, high int) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[NumberSignal](),
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			lower := low
			upper := high
			nextGuess := func() int { return (lower + upper) / 2 }
			return &workflow.Executor{
				ID: id,
				Spec: workflow.ExecutorSpec{
					DisableAutoSendMessageHandlerResultObject: true,
					DisableAutoYieldOutputHandlerResultObject: true,
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[NumberSignal](), nil, func(ctx *workflow.Context, msg any) (any, error) {
							switch msg.(NumberSignal) {
							case Above:
								upper = nextGuess() - 1
							case Below:
								lower = nextGuess() + 1
							}
							return struct{}{}, ctx.SendMessage("", nextGuess())
						})
						return rb, nil
					},
				},
			}, nil
		},
	}
}

func newJudgeExecutor(id string, target int) workflow.ExecutorBinding {
	return workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[int](),
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			tries := 0
			return &workflow.Executor{
				ID: id,
				Spec: workflow.ExecutorSpec{
					DisableAutoSendMessageHandlerResultObject: true,
					DisableAutoYieldOutputHandlerResultObject: true,
					ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
						rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[int](), nil, func(ctx *workflow.Context, msg any) (any, error) {
							guess := msg.(int)
							tries++
							switch {
							case guess == target:
								return struct{}{}, ctx.YieldOutput(fmt.Sprintf("%d found in %d tries", target, tries))
							case guess < target:
								return struct{}{}, ctx.SendMessage("", Below)
							default:
								return struct{}{}, ctx.SendMessage("", Above)
							}
						})
						return rb, nil
					},
				},
			}, nil
		},
	}
}
