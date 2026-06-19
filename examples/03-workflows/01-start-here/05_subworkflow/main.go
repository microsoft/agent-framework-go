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
	"Sub-Workflows",
	"This sample composes a parent workflow from a reusable text-processing subworkflow.",
)

func main() {
	demo.Assistant("Building subworkflow: Uppercase -> Reverse -> Append Suffix")
	uppercase := textTransformExecutor("UppercaseExecutor", func(input string) string {
		return strings.ToUpper(input)
	})
	reverse := textTransformExecutor("ReverseTextExecutor", func(input string) string {
		runes := []rune(input)
		slices.Reverse(runes)
		return string(runes)
	})
	appendSuffix := textTransformExecutor("AppendSuffixExecutor", func(input string) string {
		return input + " [PROCESSED]"
	})

	textProcessing, err := workflow.NewBuilder(uppercase).
		AddEdge(uppercase, reverse).
		AddEdge(reverse, appendSuffix).
		WithOutputFrom(appendSuffix).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	demo.Assistant("Building parent workflow: Prefix -> SubWorkflow -> PostProcess")
	prefix := textTransformExecutor("PrefixExecutor", func(input string) string {
		return "INPUT: " + input
	})
	textProcessingExecutor := inproc.BindSubworkflowAsExecutor(textProcessing, "TextProcessingSubWorkflow")
	postProcess := textTransformExecutor("PostProcessExecutor", func(input string) string {
		return "[FINAL] " + input + " [END]"
	})

	mainWorkflow, err := workflow.NewBuilder(prefix).
		AddEdge(prefix, textProcessingExecutor).
		AddEdge(textProcessingExecutor, postProcess).
		WithOutputFrom(postProcess).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()
	demo.Assistant("Executing main workflow with input: hello")
	run, err := inproc.Default.RunStreaming(ctx, mainWorkflow, "hello")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch event := evt.(type) {
		case workflow.ExecutorCompletedEvent:
			if event.Result != nil {
				demo.Assistantf("[%s] %v", event.ExecutorID, event.Result)
			}
		case workflow.OutputEvent:
			demo.Assistantf("Final output: %v", event.Output)
		case workflow.ErrorEvent:
			demo.Panic(event.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", event.ExecutorID, event.Error)
		}
	}
}

func textTransformExecutor(id string, transform func(string) string) workflow.ExecutorBinding {
	return workflow.NewExecutor(id, func(input string) string {
		output := transform(input)
		demo.Assistantf("[%s] %q -> %q", id, input, output)
		return output
	}).Bind()
}
