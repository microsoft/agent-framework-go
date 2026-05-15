// Copyright (c) Microsoft. All rights reserved.

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

var logger = demo.NewLogger(
	"Agent Workflow Patterns",
	"This sample switches between sequential and concurrent agent workflow patterns.",
	"Model", deployment,
)

func main() {
	pattern := cmp.Or(os.Getenv("WORKFLOW_PATTERN"), "sequential")
	wf, err := buildPattern(pattern)
	if err != nil {
		demo.Panic(err)
	}

	run, err := inproc.Default.RunStreaming(context.Background(), wf, message.NewText("Hello, world!"))
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(context.Background()) }()

	emitEvents := true
	if err := run.SendMessage(context.Background(), workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		demo.Panic(err)
	}
	for evt, err := range run.WatchStream(context.Background()) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			if update, ok := e.Output.(*agent.ResponseUpdate); ok {
				demo.Assistantf("%s: %s", e.ExecutorID, update.String())
			} else {
				demo.Assistantf("Output: %v", e.Output)
			}
		}
	}
}

func buildPattern(pattern string) (*workflow.Workflow, error) {
	cfg := workflowhosting.Config{DisableMessageForwarding: true, DisableRoleReassignment: true}
	french := workflowhosting.New(newTranslationAgent("French"), cfg)
	spanish := workflowhosting.New(newTranslationAgent("Spanish"), cfg)
	english := workflowhosting.New(newTranslationAgent("English"), cfg)

	switch pattern {
	case "sequential":
		return workflow.NewBuilder(french).
			AddEdge(french, spanish).
			AddEdge(spanish, english).
			WithOutputFrom(english).
			Build()
	case "concurrent":
		return workflow.NewBuilder(french).
			AddFanOutEdge(french, []workflow.ExecutorBinding{spanish, english}).
			WithOutputFrom(spanish, english).
			Build()
	default:
		return nil, fmt.Errorf("unknown WORKFLOW_PATTERN %q; use sequential or concurrent", pattern)
	}
}

func newTranslationAgent(language string) *agent.Agent {
	token := demo.AzureTokenCredential()
	return openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model:        deployment,
			Instructions: fmt.Sprintf("Translate the user's text to %s. Return only the translation.", language),
			Config: agent.Config{
				Name:        language,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
}
