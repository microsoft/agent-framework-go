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
	"Workflow Streaming",
	"This sample streams workflow events from a simple text processing pipeline.",
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

	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.ExecutorCompletedEvent:
			demo.Assistantf("%s: %v", e.ExecutorID, e.Result)
		case workflow.OutputEvent:
			demo.Assistantf("Output: %v", e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panic(e.Error)
		}
	}
}
