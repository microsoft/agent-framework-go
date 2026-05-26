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
	guess := newGuessNumberExecutor("GuessNumber", 1, 100).Bind()
	judge := newJudgeExecutor("Judge", 42).Bind()

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
		switch e := evt.(type) {
		case workflow.OutputEvent:
			demo.Assistant(e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
}

func newGuessNumberExecutor(id string, low int, high int) *workflow.Executor {
	lower, upper := low, high
	nextGuess := func() int { return (lower + upper) / 2 }
	return workflow.NewExecutor(id, func(ctx *workflow.Context, signal NumberSignal) error {
		switch signal {
		case Above:
			upper = nextGuess() - 1
		case Below:
			lower = nextGuess() + 1
		}
		return ctx.SendMessage("", nextGuess())
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[int]())
			return rb, nil
		},
		ResetFunc: func() error {
			lower, upper = low, high
			return nil
		},
	})
}

func newJudgeExecutor(id string, target int) *workflow.Executor {
	var tries int
	return workflow.NewExecutor(id, func(ctx *workflow.Context, guess int) error {
		tries++
		switch {
		case guess == target:
			return ctx.YieldOutput(fmt.Sprintf("%d found in %d tries", target, tries))
		case guess < target:
			return ctx.SendMessage("", Below)
		default:
			return ctx.SendMessage("", Above)
		}
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			rb.SendsMessageType(reflect.TypeFor[NumberSignal]())
			rb.YieldsOutputType(reflect.TypeFor[string]())
			return rb, nil
		},
		ResetFunc: func() error {
			tries = 0
			return nil
		},
	})
}
