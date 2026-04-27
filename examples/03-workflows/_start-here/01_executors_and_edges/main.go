package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

// This sample introduces the concepts of executors and edges in a workflow.
//
// Workflows are built from executors (processing units) connected by edges (data flow paths).
// In this example, we create a simple text processing pipeline that:
//  1. Takes input text and converts it to uppercase using an UppercaseExecutor
//  2. Takes the uppercase text and reverses it using a ReverseTextExecutor
//
// The executors are connected sequentially, so data flows from one to the next in order.
// For input "Hello, World!", the workflow produces "!DLROW ,OLLEH".

func main() {
	// Create the executors.
	uppercase := workflow.BindFunc("UppercaseExecutor", true, func(input string) string {
		return strings.ToUpper(input)
	})

	// Build the workflow by connecting executors sequentially.
	reverse := workflow.BindFunc("ReverseExecutor", true, func(input string) string {
		runes := []rune(input)
		slices.Reverse(runes)
		return string(runes)
	})
	wf, err := workflow.NewBuilder(uppercase).
		AddEdge(uppercase, reverse).
		WithOutputFrom(reverse).
		Build()
	if err != nil {
		panic(err)
	}

	// Execute the workflow with sample input.
	run, err := inproc.Run(context.Background(), wf, "", "Hello, World!")
	if err != nil {
		panic(err)
	}
	for evt := range run.NewEvents() {
		if evt, ok := evt.(workflow.ExecutorCompletedEvent); ok {
			fmt.Printf("%s: %v\n", evt.ExecutorID, evt.Result)
		}
	}
}
