// Copyright (c) Microsoft. All rights reserved.

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
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Human in the Loop Workflow",
	"This sample uses request ports to play a human-in-the-loop number guessing game.",
)

type NumberSignal int

const (
	Init NumberSignal = iota
	Above
	Below
)

func main() {
	guessPort := workflow.RequestPort{
		ID:       "GuessNumber",
		Request:  reflect.TypeFor[NumberSignal](),
		Response: reflect.TypeFor[int](),
	}
	ask := guessPort.Bind()
	judge := workflow.NewExecutor("Judge", &judgeExecutor{target: 42}).Bind()
	wf, err := workflow.NewBuilder(ask).
		AddEdge(ask, judge).
		AddEdge(judge, ask).
		WithOutputFrom(judge).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()
	run, err := inproc.Default.RunStreaming(ctx, wf, Init)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

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
		case workflow.OutputEvent:
			demo.Assistantf("Workflow completed with result: %v", e.Output)
			return
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panic(e.Error)
		}
	}
}

func handleExternalRequest(request *workflow.ExternalRequest, input *bufio.Scanner) (*workflow.ExternalResponse, error) {
	signal, ok := workflow.PortableValueAs[NumberSignal](request.Data)
	if !ok {
		return nil, fmt.Errorf("request %v is not supported", request.PortInfo.RequestType)
	}

	var prompt string
	switch signal {
	case Init:
		prompt = "Please provide your initial guess: "
	case Above:
		prompt = "You previously guessed too large. Please provide a new guess: "
	case Below:
		prompt = "You previously guessed too small. Please provide a new guess: "
	default:
		return nil, fmt.Errorf("unsupported number signal %d", signal)
	}

	guess, err := readIntegerFromConsole(input, prompt)
	if err != nil {
		return nil, err
	}
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

type judgeExecutor struct {
	_ workflow.AttrSendsMessage[NumberSignal]
	_ workflow.AttrYieldsOutput[string]

	target int
}

func (j *judgeExecutor) Handle(ctx *workflow.Context, guess int) error {
	value, err := ctx.ReadOrInitState("tries", "", func(context.Context, string, string) (any, error) {
		return 0, nil
	})
	if err != nil {
		return err
	}
	tries, ok := value.(int)
	if !ok {
		return fmt.Errorf("unexpected tries state type %T", value)
	}
	tries++
	if err := ctx.QueueStateUpdate("tries", "", tries); err != nil {
		return err
	}
	switch {
	case guess == j.target:
		return ctx.YieldOutput(fmt.Sprintf("%d found in %d tries!", j.target, tries))
	case guess < j.target:
		return ctx.SendMessage("", Below)
	default:
		return ctx.SendMessage("", Above)
	}
}
