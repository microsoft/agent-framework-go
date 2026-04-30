package main

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

// This sample introduces the use of AI agents as executors within a workflow.
//
// Instead of simple text processing executors, this workflow uses three translation agents:
// 1. French Agent - translates input text to French
// 2. Spanish Agent - translates French text to Spanish
// 3. English Agent - translates Spanish text back to English
//
// The agents are connected sequentially, creating a translation chain.
// This demonstrates how AI-powered components can be integrated into workflow pipelines.

func main() {
	// Create agents. Disable message forwarding and role reassignment for a
	// strict pipeline where each agent forwards only its own output.
	cfg := workflowhosting.Config{
		DisableMessageForwarding: true,
		DisableRoleReassignment:  true,
	}
	frenchAgent := workflowhosting.New(newAgent("French"), cfg)
	spanishAgent := workflowhosting.New(newAgent("Spanish"), cfg)
	englishAgent := workflowhosting.New(newAgent("English"), cfg)

	wf, err := workflow.NewBuilder(frenchAgent).
		AddEdge(frenchAgent, spanishAgent).
		AddEdge(spanishAgent, englishAgent).
		Build()
	if err != nil {
		panic(err)
	}

	// Execute the workflow with sample input.
	run, err := inproc.Stream(context.Background(), wf, "", message.NewText("Hello World"))
	if err != nil {
		panic(err)
	}
	emitEvents := true
	if err := run.SendMessage(context.Background(), workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		panic(err)
	}
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
	token := demo.AzureTokenCredential()
	return openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Instructions: fmt.Sprintf("You are a helpful assistant who translates text to %s.", language),
			},
		},
	)
}
