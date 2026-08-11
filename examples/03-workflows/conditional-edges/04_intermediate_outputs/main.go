// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Intermediate vs Terminal Outputs Workflow",
	"This sample shows how WithIntermediateOutputFrom and WithOutputFrom partition workflow outputs into progress updates and the final result.",
)

func main() {
	// A mid-graph executor emits a progress update as an intermediate output
	// and forwards its work to the next executor along the edge.
	preprocess := workflow.NewExecutor("PreprocessExecutor", func(text string) string {
		return "normalized " + strings.ToLower(text)
	}).Bind()

	// The final executor emits the terminal result of the workflow.
	summarize := workflow.NewExecutor("SummarizeExecutor", func(text string) string {
		return fmt.Sprintf("Summary of %q", text)
	}).Bind()

	wf, err := workflow.NewBuilder(preprocess).
		AddEdge(preprocess, summarize).
		WithIntermediateOutputFrom(preprocess).
		WithOutputFrom(summarize).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, "Hello Workflow")
	if err != nil {
		demo.Panic(err)
	}
	for evt := range run.NewEvents() {
		output, ok := evt.(workflow.OutputEvent)
		if !ok {
			continue
		}
		if output.IsIntermediate() {
			demo.Assistant("[intermediate]", output.Output)
		} else {
			demo.Assistant("[final]", output.Output)
		}
	}
}
