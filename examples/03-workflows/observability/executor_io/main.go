// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Workflow Executor I/O Observation",
	"This sample observes per-executor input and output by handling ExecutorInvokedEvent and ExecutorCompletedEvent.",
)

func main() {
	uppercase := workflow.NewExecutor("UppercaseExecutor", func(input string) string {
		return strings.ToUpper(input)
	}).Bind()

	reverse := workflow.NewExecutor("ReverseTextExecutor", func(input string) string {
		runes := []rune(input)
		slices.Reverse(runes)
		return string(runes)
	}).Bind()

	wf, err := workflow.NewBuilder(uppercase).
		AddEdge(uppercase, reverse).
		WithOutputFrom(reverse).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.RunStreaming(context.Background(), wf, "Hello, World!")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(context.Background()) }()

	// Unlike 01_streaming, this loop surfaces the input each executor receives via
	// ExecutorInvokedEvent, not just the output via ExecutorCompletedEvent. Pairing
	// the two events gives a full per-node I/O trace of the workflow.
	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.ExecutorInvokedEvent:
			demo.Assistantf("invoked %s: %v", e.ExecutorID, e.Message)
		case workflow.ExecutorCompletedEvent:
			demo.Assistantf("completed %s: %v", e.ExecutorID, e.Result)
		case workflow.OutputEvent:
			demo.Assistantf("Output: %v", e.Output)
		case workflow.ExecutorFailedEvent:
			demo.Panic(e.Error)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		}
	}
}
