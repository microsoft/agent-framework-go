package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework/go/workflow"
	"github.com/microsoft/agent-framework/go/workflow/inproc"
)

// This sample introduces the use of AI agents as executors within a workflow.
//
// Instead of simple text processing executors, this workflow uses three translation agents:
// 1. French Agent - translates input text to French
// 2. Spanish Agent - translates French text to Spanish
// 3. English Agent - translates Spanish text back to English
//
// The agents are connected sequentially, creating a translation chain that demonstrates
// how AI-powered components can be seamlessly integrated into workflow pipelines.

func main() {
	// Create the executors
	uppercase := workflow.BindFunc("UppercaseExecutor", true, func(input string) string {
		return strings.ToUpper(input)
	})

	// Build the workflow by connecting executors sequentially
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

	// Execute the workflow with sample input
	run, err := inproc.Stream(context.Background(), wf, "", "Hello, World!")
	if err != nil {
		panic(err)
	}
	for evt := range run.WatchStream(context.Background()) {
		if evt, ok := evt.(workflow.ExecutorCompletedEvent); ok {
			fmt.Printf("%s: %v\n", evt.ExecutorID, evt.Result)
		}
	}
}
