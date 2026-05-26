package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/checkpoint"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Checkpoint with Human in the Loop",
	"This sample checkpoints a number guessing workflow across request/response turns.",
)

type NumberSignal int

const (
	Init NumberSignal = iota
	Above
	Below
)

type SignalWithNumber struct {
	Signal NumberSignal
	Number int
}

func main() {
	wf := buildWorkflow()
	manager := checkpoint.NewInMemoryManager()
	ctx := context.Background()

	run, err := inproc.Default.WithCheckpointing(manager).RunStreaming(ctx, wf, SignalWithNumber{Signal: Init})
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	var checkpoints []workflow.CheckpointInfo
	input := bufio.NewScanner(os.Stdin)
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.RequestInfoEvent:
			response, err := handleExternalRequest(e.Request, input)
			if err != nil {
				demo.Panic(err)
			}
			if err := run.SendResponse(ctx, response); err != nil {
				demo.Panic(err)
			}
		case workflow.ExecutorCompletedEvent:
			demo.Assistantf("Executor %s completed.", e.ExecutorID)
		case workflow.SuperStepCompletedEvent:
			if e.CompletionInfo != nil && e.CompletionInfo.CheckpointInfo != nil {
				checkpoints = append(checkpoints, *e.CompletionInfo.CheckpointInfo)
				demo.Assistantf("Checkpoint created at step %d: %s", len(checkpoints), e.CompletionInfo.CheckpointInfo.CheckpointID)
			}
		case workflow.OutputEvent:
			demo.Assistantf("Workflow completed with result: %v", e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panic(e.Error)
		}
	}

	const checkpointIndex = 1
	if len(checkpoints) <= checkpointIndex {
		demo.Panicf("expected at least %d checkpoints", checkpointIndex+1)
	}
	demo.Assistantf("Number of checkpoints created: %d", len(checkpoints))
	savedCheckpoint := checkpoints[checkpointIndex]
	demo.Assistantf("Restoring from checkpoint %s", savedCheckpoint.CheckpointID)
	if err := run.RestoreCheckpoint(ctx, savedCheckpoint); err != nil {
		demo.Panic(err)
	}
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.RequestInfoEvent:
			response, err := handleExternalRequest(e.Request, input)
			if err != nil {
				demo.Panic(err)
			}
			if err := run.SendResponse(ctx, response); err != nil {
				demo.Panic(err)
			}
		case workflow.ExecutorCompletedEvent:
			demo.Assistantf("Executor %s completed.", e.ExecutorID)
		case workflow.OutputEvent:
			demo.Assistantf("Restored run completed with result: %v", e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panic(e.Error)
		}
	}
}

func handleExternalRequest(request *workflow.ExternalRequest, input *bufio.Scanner) (*workflow.ExternalResponse, error) {
	signal, ok := workflow.PortableValueAs[SignalWithNumber](request.Data)
	if !ok {
		return nil, fmt.Errorf("request %v is not supported", request.PortInfo.RequestType)
	}

	var prompt string
	switch signal.Signal {
	case Init:
		prompt = "Please provide your initial guess: "
	case Above:
		prompt = fmt.Sprintf("You previously guessed %d too large. Please provide a new guess: ", signal.Number)
	case Below:
		prompt = fmt.Sprintf("You previously guessed %d too small. Please provide a new guess: ", signal.Number)
	default:
		return nil, fmt.Errorf("unsupported number signal %d", signal.Signal)
	}

	guess, err := readIntegerFromConsole(input, prompt)
	if err != nil {
		return nil, err
	}
	demo.Assistantf("Human response: %d", guess)
	return request.CreateResponse(guess)
}

func readIntegerFromConsole(input *bufio.Scanner, prompt string) (int, error) {
	for {
		fmt.Print(prompt)
		if !input.Scan() {
			if err := input.Err(); err != nil {
				return 0, err
			}
			return 0, fmt.Errorf("input stream closed")
		}
		value, err := strconv.Atoi(strings.TrimSpace(input.Text()))
		if err == nil {
			return value, nil
		}
		fmt.Println("Invalid input. Please enter a valid integer.")
	}
}

func buildWorkflow() *workflow.Workflow {
	guessPort := workflow.RequestPort{
		ID:       "GuessPort",
		Request:  reflect.TypeFor[SignalWithNumber](),
		Response: reflect.TypeFor[int](),
	}
	ask := guessPort.Bind()
	judgeState := &judgeExecutor{target: 42}
	judge := workflow.NewExecutor("Judge", judgeState).Extend(&workflow.Executor{
		ResetFunc:                judgeState.Reset,
		OnCheckpointFunc:         judgeState.OnCheckpoint,
		OnCheckpointRestoredFunc: judgeState.OnCheckpointRestored,
	}).Bind()
	wf, err := workflow.NewBuilder(ask).
		AddEdge(ask, judge).
		AddEdge(judge, ask).
		WithOutputFrom(judge).
		Build()
	if err != nil {
		demo.Panic(err)
	}
	return wf
}

const judgeExecutorStateKey = "JudgeExecutorState"

type judgeExecutor struct {
	_ workflow.AttrSendsMessage[SignalWithNumber]
	_ workflow.AttrYieldsOutput[string]

	target int
	tries  int
}

func (j *judgeExecutor) Handle(ctx *workflow.Context, guess int) error {
	j.tries++
	switch {
	case guess == j.target:
		return ctx.YieldOutput(fmt.Sprintf("%d found in %d tries", j.target, j.tries))
	case guess < j.target:
		return ctx.SendMessage("", SignalWithNumber{Signal: Below, Number: guess})
	default:
		return ctx.SendMessage("", SignalWithNumber{Signal: Above, Number: guess})
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
