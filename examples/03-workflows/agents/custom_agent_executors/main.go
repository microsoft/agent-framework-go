// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var logger = demo.NewLogger(
	"Custom Agent Executors",
	"This sample wraps Microsoft Foundry agents in custom workflow executors with loop control.",
	"Model", demo.FoundryModel,
)

type SloganResult struct {
	Task   string `json:"task"`
	Slogan string `json:"slogan"`
}

type FeedbackResult struct {
	Comments string `json:"comments"`
	Rating   int    `json:"rating"`
	Actions  string `json:"actions"`
}

type SloganGeneratedEvent struct{ Result SloganResult }

type FeedbackEvent struct{ Result FeedbackResult }

func (e SloganGeneratedEvent) Data() any { return e.Result }

func (e FeedbackEvent) Data() any { return e.Result }

func main() {
	sloganWriter := newSloganWriter("SloganWriter")
	feedbackProvider := newFeedbackProvider("FeedbackProvider", 8, 3)

	wf, err := workflow.NewBuilder(sloganWriter).
		AddEdge(sloganWriter, feedbackProvider).
		AddEdge(feedbackProvider, sloganWriter).
		WithOutputFrom(feedbackProvider).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.RunStreaming(context.Background(), wf, "Create a slogan for a new electric SUV that is affordable and fun to drive.")
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(context.Background()) }()

	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case SloganGeneratedEvent:
			demo.Assistantf("Slogan: %s", e.Result.Slogan)
		case FeedbackEvent:
			demo.Assistantf("Feedback: rating=%d comments=%s actions=%s", e.Result.Rating, e.Result.Comments, e.Result.Actions)
		case workflow.OutputEvent:
			demo.Assistant(e.Output)
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panic(e.Error)
		}
	}
}

func newSloganWriter(id string) workflow.ExecutorBinding {
	token := demo.FoundryTokenCredential()
	ag := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a professional slogan writer. Return structured output with task and slogan.",
			Config: agent.Config{
				Name:        id,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.
					AddHandlerRaw(reflect.TypeFor[string](), reflect.TypeFor[SloganResult](), func(ctx *workflow.Context, msg any) (any, error) {
						return writeSlogan(ctx, ag, msg.(string))
					}).
					AddHandlerRaw(reflect.TypeFor[FeedbackResult](), reflect.TypeFor[SloganResult](), func(ctx *workflow.Context, msg any) (any, error) {
						feedback := msg.(FeedbackResult)
						prompt := fmt.Sprintf("Improve the previous slogan using this feedback. Comments: %s Rating: %d Actions: %s", feedback.Comments, feedback.Rating, feedback.Actions)
						return writeSlogan(ctx, ag, prompt)
					})
				return rb, nil
			},
		}, nil
	})
}

func writeSlogan(ctx *workflow.Context, ag *agent.Agent, prompt string) (SloganResult, error) {
	var result SloganResult
	_, err := ag.RunText(ctx, prompt, agent.WithStructuredOutput(&result)).Collect()
	if err != nil {
		return SloganResult{}, err
	}
	if result.Task == "" {
		result.Task = prompt
	}
	if err := ctx.AddEvent(SloganGeneratedEvent{Result: result}); err != nil {
		return SloganResult{}, err
	}
	return result, nil
}

func newFeedbackProvider(id string, minimumRating int, maxAttempts int) workflow.ExecutorBinding {
	token := demo.FoundryTokenCredential()
	ag := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a professional editor. Return structured output with comments, rating from 1 to 10, and actions.",
			Config: agent.Config{
				Name:        id,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	return workflow.BindNewExecutorFunc(id, func(_ string, executorID string) (*workflow.Executor, error) {
		attempts := 0
		return &workflow.Executor{
			ID: executorID,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(reflect.TypeFor[FeedbackResult]())
				rb.YieldsOutputType(reflect.TypeFor[string]())
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[SloganResult](), nil, func(ctx *workflow.Context, msg any) (any, error) {
					slogan := msg.(SloganResult)
					prompt := fmt.Sprintf("Task: %s\nSlogan: %s\nProvide feedback.", slogan.Task, slogan.Slogan)
					var feedback FeedbackResult
					_, err := ag.RunText(ctx, prompt, agent.WithStructuredOutput(&feedback)).Collect()
					if err != nil {
						return struct{}{}, err
					}
					if err := ctx.AddEvent(FeedbackEvent{Result: feedback}); err != nil {
						return struct{}{}, err
					}
					if feedback.Rating >= minimumRating {
						return struct{}{}, ctx.YieldOutput("The following slogan was accepted:\n\n" + slogan.Slogan)
					}
					if attempts >= maxAttempts {
						return struct{}{}, ctx.YieldOutput(fmt.Sprintf("The slogan was rejected after %d attempts. Final slogan:\n\n%s", maxAttempts, slogan.Slogan))
					}
					attempts++
					return struct{}{}, ctx.SendMessage("", feedback)
				})
				return rb, nil
			},
		}, nil
	})
}
