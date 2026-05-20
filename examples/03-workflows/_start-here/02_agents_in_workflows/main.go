// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/agent/provider/openaiagent"
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
	"Agents in Workflows",
	"This sample runs translation agents as workflow executors.",
	"Model", deployment,
)

func main() {
	cfg := workflowhosting.Config{
		DisableForwardIncomingMessages:    true,
		DisableReassignOtherAgentsAsUsers: true,
	}
	french := workflowhosting.New(newTranslationAgent("French"), cfg)
	spanish := workflowhosting.New(newTranslationAgent("Spanish"), cfg)
	english := workflowhosting.New(newTranslationAgent("English"), cfg)

	wf, err := workflow.NewBuilder(french).
		AddEdge(french, spanish).
		AddEdge(spanish, english).
		WithOutputFrom(english).
		Build()
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()

	run, err := inproc.Default.RunStreaming(ctx, wf, message.NewText("Hello World"))
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	emitEvents := true
	if err := run.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		demo.Panic(err)
	}
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		if out, ok := evt.(workflow.OutputEvent); ok {
			if update, ok := out.Output.(*agent.ResponseUpdate); ok {
				demo.Assistantf("%s: %s", out.ExecutorID, update.String())
			}
		}
	}
}

func newTranslationAgent(language string) *agent.Agent {
	token := demo.AzureTokenCredential()
	return openaiagent.NewChatCompletions(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiagent.Config{
			Model:        deployment,
			Instructions: fmt.Sprintf("Translate the user's text to %s. Return only the translation.", language),
			Config: agent.Config{
				Name:        language,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
}
