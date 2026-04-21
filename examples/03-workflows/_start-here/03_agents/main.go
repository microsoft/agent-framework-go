package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
	"github.com/openai/openai-go/v3"
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
	// Create agents
	frenchAgent := newAgent("French").Bind(false)
	spanishAgent := newAgent("Spanish").Bind(false)
	englishAgent := newAgent("English").Bind(false)

	wf, err := workflow.NewBuilder(frenchAgent).
		AddEdge(frenchAgent, spanishAgent).
		AddEdge(spanishAgent, englishAgent).
		Build()

	if err != nil {
		panic(err)
	}

	// Execute the workflow with sample input
	run, err := inproc.Stream(context.Background(), wf, "", message.NewText("Hello World"))
	if err != nil {
		panic(err)
	}
	emitEvents := true
	run.SendMessage(context.Background(), workflow.TurnToken{EmitEvents: &emitEvents})
	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			panic(err)
		}
		if evt, ok := evt.(workflow.ResponseUpdateEvent); ok {
			fmt.Printf("%s: %v\n", evt.ExecutorID, evt.Update)
		}
	}
}

func newAgent(language string) *agent.Agent {
	return openaichatagent.New(
		openai.NewClient(),
		openaichatagent.Config{
			Model: "gpt-5-nano",
			Config: agent.Config{
				Instructions: fmt.Sprintf("You are a helpful assistant who translates text to %s.", language),
			},
		})
}
