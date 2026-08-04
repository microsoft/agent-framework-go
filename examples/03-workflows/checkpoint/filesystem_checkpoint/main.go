// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Filesystem Checkpoint Workflow",
	"This sample persists workflow checkpoints to disk with the filesystem JSON store, "+
		"closes the store, reopens it in a fresh process-like session, and resumes the workflow to completion.",
)

type NumberSignal int

const (
	Init NumberSignal = iota
	Above
	Below
)

func main() {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "filesystem_checkpoint")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	demo.Assistantf("Using checkpoint directory: %s", dir)

	// Phase one: run the workflow to completion, persisting checkpoints to disk,
	// then close the store to release its file handles and process lock.
	checkpoints := runPhaseOne(ctx, dir)
	if len(checkpoints) == 0 {
		demo.Panic("no checkpoints were persisted during phase one")
	}
	demo.Assistantf("Persisted %d checkpoint(s) to disk before closing the store.", len(checkpoints))
	listCheckpointFiles(dir)

	// Phase two: reopen the store from disk in a fresh manager and resume from a
	// persisted checkpoint to completion, proving the durability of the
	// filesystem-backed backend across a close/reopen.
	runPhaseTwo(ctx, dir, checkpoints)
}

// runPhaseOne runs the workflow to completion with a filesystem-backed
// checkpoint manager, collecting every checkpoint persisted along the way, then
// closes the store so nothing is left in memory. Consuming the whole stream
// guarantees the run loop has finished and every checkpoint is durably on disk
// before the store closes, so the on-disk state matches what we report and what
// phase two later resumes from.
func runPhaseOne(ctx context.Context, dir string) []workflow.CheckpointInfo {
	store, err := checkpoint.NewFileSystemJSONStore(dir)
	if err != nil {
		demo.Panic(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			demo.Panic(err)
		}
		demo.Assistantf("Closed the checkpoint store.")
	}()

	mgr := checkpoint.NewJSONManager(store)
	run, err := inproc.Default.WithCheckpointing(mgr).RunStreaming(ctx, buildWorkflow(), Init)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	var checkpoints []workflow.CheckpointInfo
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
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
	return checkpoints
}

// runPhaseTwo reopens the store from disk, reads the persisted checkpoint index,
// and resumes the latest checkpoint to completion.
func runPhaseTwo(ctx context.Context, dir string, phaseOne []workflow.CheckpointInfo) {
	store, err := checkpoint.NewFileSystemJSONStore(dir)
	if err != nil {
		demo.Panic(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			demo.Panic(err)
		}
	}()
	demo.Assistantf("Reopened checkpoint store from disk.")

	sessionID := phaseOne[0].SessionID
	persisted, err := store.RetrieveIndex(ctx, sessionID, nil)
	if err != nil {
		demo.Panic(err)
	}
	demo.Assistantf("Retrieved %d persisted checkpoint(s) from disk.", len(persisted))
	if len(persisted) == 0 {
		demo.Panic("expected persisted checkpoints after reopening the store")
	}

	// Resume from an intermediate checkpoint recorded during phase one, picking
	// up a partially completed run from durable storage and driving it to the end.
	const resumeFromStep = 3
	if len(phaseOne) < resumeFromStep {
		demo.Panicf("expected at least %d checkpoints from phase one", resumeFromStep)
	}
	resumeFrom := phaseOne[resumeFromStep-1]
	demo.Assistantf("Resuming from checkpoint %s.", resumeFrom.CheckpointID)

	mgr := checkpoint.NewJSONManager(store)
	run, err := inproc.Default.WithCheckpointing(mgr).ResumeStreaming(ctx, buildWorkflow(), resumeFrom)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	for evt, err := range run.WatchStream(ctx) {
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

// listCheckpointFiles prints the files the store wrote under dir so the durable
// on-disk layout (the index.jsonl plus one file per checkpoint) is visible.
func listCheckpointFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		demo.Panic(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	demo.Assistantf("Files written under %s:", dir)
	for _, name := range names {
		demo.Assistantf("  - %s", filepath.Join(dir, name))
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
