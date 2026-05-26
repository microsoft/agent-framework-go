// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Checkpoint and Rehydrate Workflow",
	"This sample saves workflow checkpoints and rehydrates a new workflow instance from a saved checkpoint.",
)

type NumberSignal int

const (
	Init NumberSignal = iota
	Above
	Below
)

func main() {
	wf := buildWorkflow()
	checkpointManager := checkpoint.NewInMemoryManager()
	ctx := context.Background()

	var checkpoints []workflow.CheckpointInfo
	checkpointedRun, err := inproc.Default.WithCheckpointing(checkpointManager).RunStreaming(ctx, wf, Init)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = checkpointedRun.Close(ctx) }()

	for evt, err := range checkpointedRun.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.ExecutorCompletedEvent:
			demo.Assistantf("* Executor %s completed.", e.ExecutorID)
		case workflow.SuperStepCompletedEvent:
			if e.CompletionInfo != nil && e.CompletionInfo.CheckpointInfo != nil {
				checkpoints = append(checkpoints, *e.CompletionInfo.CheckpointInfo)
				demo.Assistantf("** Checkpoint created at step %d.", len(checkpoints))
			}
		case workflow.OutputEvent:
			demo.Assistantf("Workflow completed with result: %v", e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}

	if len(checkpoints) == 0 {
		demo.Panic("no checkpoints were created during the workflow execution")
	}
	demo.Assistantf("Number of checkpoints created: %d", len(checkpoints))

	newWorkflow := buildWorkflow()
	const checkpointIndex = 5
	if len(checkpoints) <= checkpointIndex {
		demo.Panicf("expected at least %d checkpoints", checkpointIndex+1)
	}
	demo.Assistantf("Hydrating a new workflow instance from the %dth checkpoint.", checkpointIndex+1)
	savedCheckpoint := checkpoints[checkpointIndex]

	newCheckpointedRun, err := inproc.Default.WithCheckpointing(checkpointManager).ResumeStreaming(ctx, newWorkflow, savedCheckpoint)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = newCheckpointedRun.Close(ctx) }()

	for evt, err := range newCheckpointedRun.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.ExecutorCompletedEvent:
			demo.Assistantf("* Executor %s completed.", e.ExecutorID)
		case workflow.OutputEvent:
			demo.Assistantf("Workflow completed with result: %v", e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
}

func buildWorkflow() *workflow.Workflow {
	guessNumberExecutor := newGuessNumberExecutor(1, 100).Bind()
	judgeExecutor := newJudgeExecutor(42).Bind()

	wf, err := workflow.NewBuilder(guessNumberExecutor).
		AddEdge(guessNumberExecutor, judgeExecutor).
		AddEdge(judgeExecutor, guessNumberExecutor).
		WithOutputFrom(judgeExecutor).
		Build()
	if err != nil {
		demo.Panic(err)
	}
	return wf
}

const guessNumberExecutorStateKey = "GuessNumberExecutorState"

type guessNumberExecutorState struct {
	LowerBound int
	UpperBound int
}

type guessNumberExecutor struct {
	_ workflow.AttrSendsMessage[int]

	initialLowerBound int
	initialUpperBound int
	lowerBound        int
	upperBound        int
}

func newGuessNumberExecutor(lowerBound int, upperBound int) *workflow.Executor {
	executor := &guessNumberExecutor{
		initialLowerBound: lowerBound,
		initialUpperBound: upperBound,
	}
	if err := executor.Reset(); err != nil {
		demo.Panic(err)
	}
	return workflow.NewExecutor("Guess", executor).Extend(&workflow.Executor{
		ResetFunc:                executor.Reset,
		OnCheckpointFunc:         executor.OnCheckpoint,
		OnCheckpointRestoredFunc: executor.OnCheckpointRestored,
	})
}

func (g *guessNumberExecutor) Handle(ctx *workflow.Context, signal NumberSignal) error {
	switch signal {
	case Init:
	case Above:
		g.upperBound = g.nextGuess() - 1
	case Below:
		g.lowerBound = g.nextGuess() + 1
	default:
		return fmt.Errorf("unsupported number signal %d", signal)
	}
	return ctx.SendMessage("", g.nextGuess())
}

func (g *guessNumberExecutor) Reset() error {
	g.lowerBound = g.initialLowerBound
	g.upperBound = g.initialUpperBound
	return nil
}

func (g *guessNumberExecutor) OnCheckpoint(ctx *workflow.Context) error {
	return ctx.QueueStateUpdate(guessNumberExecutorStateKey, "", guessNumberExecutorState{
		LowerBound: g.lowerBound,
		UpperBound: g.upperBound,
	})
}

func (g *guessNumberExecutor) OnCheckpointRestored(ctx *workflow.Context) error {
	state, err := ctx.ReadState(guessNumberExecutorStateKey, "")
	if err != nil {
		return err
	}
	if state == nil {
		return g.Reset()
	}
	restoredState, ok := state.(guessNumberExecutorState)
	if !ok {
		return fmt.Errorf("unexpected guess number executor state type %T", state)
	}
	g.lowerBound = restoredState.LowerBound
	g.upperBound = restoredState.UpperBound
	return nil
}

func (g *guessNumberExecutor) nextGuess() int {
	return (g.lowerBound + g.upperBound) / 2
}

const judgeExecutorStateKey = "JudgeExecutorState"

type judgeExecutor struct {
	_ workflow.AttrSendsMessage[NumberSignal]
	_ workflow.AttrYieldsOutput[string]

	targetNumber int
	tries        int
}

func newJudgeExecutor(targetNumber int) *workflow.Executor {
	executor := &judgeExecutor{targetNumber: targetNumber}
	return workflow.NewExecutor("Judge", executor).Extend(&workflow.Executor{
		ResetFunc:                executor.Reset,
		OnCheckpointFunc:         executor.OnCheckpoint,
		OnCheckpointRestoredFunc: executor.OnCheckpointRestored,
	})
}

func (j *judgeExecutor) Handle(ctx *workflow.Context, guess int) error {
	j.tries++
	switch {
	case guess == j.targetNumber:
		return ctx.YieldOutput(fmt.Sprintf("%d found in %d tries!", j.targetNumber, j.tries))
	case guess < j.targetNumber:
		return ctx.SendMessage("", Below)
	default:
		return ctx.SendMessage("", Above)
	}
}

func (j *judgeExecutor) Reset() error {
	j.tries = 0
	return nil
}

func (j *judgeExecutor) OnCheckpoint(ctx *workflow.Context) error {
	return ctx.QueueStateUpdate(judgeExecutorStateKey, "", j.tries)
}

func (j *judgeExecutor) OnCheckpointRestored(ctx *workflow.Context) error {
	state, err := ctx.ReadState(judgeExecutorStateKey, "")
	if err != nil {
		return err
	}
	if state == nil {
		j.tries = 0
		return nil
	}
	tries, ok := state.(int)
	if !ok {
		return fmt.Errorf("unexpected judge executor state type %T", state)
	}
	j.tries = tries
	return nil
}
