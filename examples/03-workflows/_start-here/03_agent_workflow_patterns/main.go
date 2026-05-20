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

	if _, err := runWorkflow(context.Background(), wf, []*message.Message{message.NewText("Hello, world!")}); err != nil {
		demo.Panic(err)
	}
}

func runWorkflow(ctx context.Context, wf *workflow.Workflow, messages []*message.Message) ([]*message.Message, error) {
	run, err := inproc.Default.RunStreaming(ctx, wf, messages)
	if err != nil {
		return nil, err
	}
	defer func() { _ = run.Close(ctx) }()

	emitEvents := true
	if err := run.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		return nil, err
	}

	lastExecutorID := ""
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			return nil, err
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			if update, ok := e.Output.(*agent.ResponseUpdate); ok {
				if e.ExecutorID != lastExecutorID {
					lastExecutorID = e.ExecutorID
					demo.Assistantf("%s", e.ExecutorID)
				}
				if updateText := update.String(); updateText != "" {
					demo.Assistantf("%s", updateText)
				}
				continue
			}
			if outputMessages, ok := e.Output.([]*message.Message); ok {
				return outputMessages, nil
			}
		case workflow.ErrorEvent:
			return nil, e.Error
		case workflow.ExecutorFailedEvent:
			return nil, fmt.Errorf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
	return nil, nil
}

func buildPattern(pattern string) (*workflow.Workflow, error) {
	agents := []*agent.Agent{
		newTranslationAgent("French"),
		newTranslationAgent("Spanish"),
		newTranslationAgent("English"),
	}

	switch pattern {
	case "sequential":
		return workflowhosting.BuildSequential("", agents...)
	case "concurrent":
		return workflowhosting.BuildConcurrent("", agents...)
	default:
		return nil, fmt.Errorf("unknown WORKFLOW_PATTERN %q; use sequential or concurrent", pattern)
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
			Instructions: fmt.Sprintf("You are a translation assistant who only responds in %s. Respond to any input by outputting the name of the input language and then translating the input to %s.", language, language),
			Config: agent.Config{
				Name:        language,
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
}
