// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
)

var logger = demo.NewLogger(
	"Workflow as an Agent",
	"This sample wraps a concurrent workflow so callers interact with it as one agent.",
	"Model", demo.Deployment,
)

func main() {
	french := demo.NewAzureChatAgent("French", "Respond in French. Keep the answer concise.", logger)
	english := demo.NewAzureChatAgent("English", "Respond in English. Keep the answer concise.", logger)

	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(french, english).WithName("bilingual-workflow").Build()
	if err != nil {
		demo.Panic(err)
	}
	wfAgent, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{
		IncludeOutputsInResponse: true,
		Config: agent.Config{
			Name: "BilingualWorkflowAgent",
		},
	})
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()
	session, err := wfAgent.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for _, input := range []string{"What makes a good city park?", "Give one practical design principle."} {
		demo.Assistantf("User: %s", input)
		for update, err := range wfAgent.RunText(ctx, input, agent.WithSession(session), agent.Stream(true)) {
			if err != nil {
				demo.Panic(err)
			}
			if text := update.String(); text != "" {
				demo.Assistantf("%s", text)
			}
		}
	}
}
