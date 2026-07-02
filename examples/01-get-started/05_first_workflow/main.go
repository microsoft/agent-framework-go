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
	"First Workflow",
	"Runs a sample string through uppercase and reverse executors.",
)

// Workflows are built from executors (processing units) connected by edges (data flow paths).
// In this example, we create a simple text processing pipeline that:
//   1. Takes input text and converts it to uppercase using an UppercaseExecutor
//   2. Takes the uppercase text and reverses it using a ReverseTextExecutor
//
// The executors are connected sequentially, so data flows from one to the next in order.
// For input "Hello, World!", the workflow produces "!DLROW ,OLLEH".

func main() {
	// Create the executors.
	uppercase := workflow.NewExecutor("UppercaseExecutor", func(input string) string {
		return strings.ToUpper(input)
	}).Bind()

	reverse := workflow.NewExecutor("ReverseExecutor", func(input string) string {
		runes := []rune(input)
		slices.Reverse(runes)
		return string(runes)
	}).Bind()

	// Build the workflow by connecting executors sequentially.
	wf, err := workflow.NewBuilder(uppercase).
		AddEdge(uppercase, reverse).
		WithOutputFrom(reverse).
		Build()
	if err != nil {
		demo.Panicf("failed to build workflow: %v", err)
	}

	// Execute the workflow with sample input.
	sampleInput := "Hello, World!"
	demo.Assistantf("Input: %q", sampleInput)
	run, err := inproc.Default.Run(context.Background(), wf, sampleInput)
	if err != nil {
		demo.Panicf("failed to run workflow: %v", err)
	}
	for evt := range run.NewEvents() {
		if evt, ok := evt.(workflow.ExecutorCompletedEvent); ok {
			demo.Assistantf("%s: %v", evt.ExecutorID, evt.Result)
		}
	}
}
