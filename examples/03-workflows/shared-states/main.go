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
	"Shared State Workflow",
	"This sample fans out over shared workflow state and aggregates file statistics.",
)

const (
	fileContentScope = "FileContentState"
)

type FileStats struct {
	ParagraphCount int
	WordCount      int
}

const sampleDocument = `Agent Framework workflows coordinate work across executors.

Shared state lets one executor store a value that later executors can read without copying the whole payload through every edge.

Fan-out and fan-in patterns make it natural to compute independent statistics and aggregate them at the end.`

func main() {
	fileRead := workflow.NewExecutor("FileReadExecutor", func(ctx *workflow.Context, filename string) (string, error) {
		fileID := "sample-file"
		if err := ctx.QueueStateUpdate(fileID, fileContentScope, sampleDocument); err != nil {
			return "", err
		}
		demo.Assistantf("Read %s into shared state as %s", filename, fileID)
		return fileID, nil
	}).Bind()

	wordCount := workflow.NewExecutor("WordCountingExecutor", func(ctx *workflow.Context, fileID string) (FileStats, error) {
		content, err := readFileContent(ctx, fileID)
		if err != nil {
			return FileStats{}, err
		}
		return FileStats{WordCount: len(strings.Fields(content))}, nil
	}).Bind()

	paragraphCount := workflow.NewExecutor("ParagraphCountingExecutor", func(ctx *workflow.Context, fileID string) (FileStats, error) {
		content, err := readFileContent(ctx, fileID)
		if err != nil {
			return FileStats{}, err
		}
		paragraphs := 0
		for _, block := range strings.Split(content, "\n\n") {
			if strings.TrimSpace(block) != "" {
				paragraphs++
			}
		}
		return FileStats{ParagraphCount: paragraphs}, nil
	}).Bind()

	aggregate := newAggregationExecutor()

	wf, err := workflow.NewBuilder(fileRead).
		AddFanOutEdge(fileRead, []workflow.ExecutorBinding{wordCount, paragraphCount}).
		AddFanInBarrierEdge([]workflow.ExecutorBinding{wordCount, paragraphCount}, aggregate).
		WithOutputFrom(aggregate).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.Run(context.Background(), wf, "Lorem_Ipsum.txt")
	if err != nil {
		demo.Panic(err)
	}
	for evt := range run.NewEvents() {
		if output, ok := evt.(workflow.OutputEvent); ok {
			demo.Assistant(output.Output)
		}
	}
}

func readFileContent(ctx *workflow.Context, fileID string) (string, error) {
	value, err := ctx.ReadState(fileID, fileContentScope)
	if err != nil {
		return "", err
	}
	content, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("file content has type %T", value)
	}
	return content, nil
}

type aggregationExecutor struct {
	collected []FileStats
}

func newAggregationExecutor() workflow.ExecutorBinding {
	return workflow.BindNewExecutorFunc("AggregationExecutor", func(_ string, executorID string) (*workflow.Executor, error) {
		aggregate := &aggregationExecutor{}
		return workflow.NewExecutor(executorID, aggregate.Handle).Extend(&workflow.Executor{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(reflect.TypeFor[string]())
				return rb, nil
			},
		}), nil
	})
}

func (e *aggregationExecutor) Handle(ctx *workflow.Context, stats FileStats) error {
	e.collected = append(e.collected, stats)
	if len(e.collected) != 2 {
		return nil
	}

	var total FileStats
	for _, stats := range e.collected {
		total.ParagraphCount += stats.ParagraphCount
		total.WordCount += stats.WordCount
	}
	return ctx.YieldOutput(fmt.Sprintf("Total Paragraphs: %d, Total Words: %d", total.ParagraphCount, total.WordCount))
}
