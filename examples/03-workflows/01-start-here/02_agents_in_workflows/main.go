// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var logger = demo.NewLogger(
	"Agents in Workflows",
	"This sample runs translation agents as workflow executors.",
	"Model", demo.Deployment,
)

func main() {
	cfg := workflowhosting.Config{
		DisableForwardIncomingMessages: true,
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
	return demo.NewAzureChatAgent(
		language,
		fmt.Sprintf("Translate the user's text to %s. Return only the translation.", language),
		logger,
	)
}
