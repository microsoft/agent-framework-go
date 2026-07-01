// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

const topic = "Electric bicycles make city commuting better."

var logger = demo.NewLogger(
	"Multi Service Workflow",
	"This sample coordinates several Microsoft Foundry agents with distinct roles in one workflow.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	agents := []*agent.Agent{
		foundryprovider.NewAgent(
			demo.FoundryProjectEndpoint,
			token,
			foundryprovider.ModelDeployment(demo.FoundryModel),
			foundryprovider.AgentConfig{
				Instructions: "Write a concise three-paragraph overview of the user's topic. Include one claim that should be fact checked.",
				Config: agent.Config{
					Name:        "researcher",
					Middlewares: []agent.Middleware{logger},
				},
			},
		),
		foundryprovider.NewAgent(
			demo.FoundryProjectEndpoint,
			token,
			foundryprovider.ModelDeployment(demo.FoundryModel),
			foundryprovider.AgentConfig{
				Instructions: "Review the prior essay. Identify supported, questionable, and false claims in concise bullets.",
				Config: agent.Config{
					Name:        "fact_checker",
					Middlewares: []agent.Middleware{logger},
				},
			},
		),
		foundryprovider.NewAgent(
			demo.FoundryProjectEndpoint,
			token,
			foundryprovider.ModelDeployment(demo.FoundryModel),
			foundryprovider.AgentConfig{
				Instructions: "Write a final single-paragraph summary using only claims that survived the fact check.",
				Config: agent.Config{
					Name:        "reporter",
					Middlewares: []agent.Middleware{logger},
				},
			},
		),
	}
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(agents...).WithName("multi-service-workflow").Build()
	if err != nil {
		demo.Panic(err)
	}

	ctx := context.Background()
	run, err := inproc.Default.RunStreaming(ctx, wf, message.NewText(topic))
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	emitEvents := true
	if err := run.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		demo.Panic(err)
	}

	lastExecutorID := ""
	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch e := evt.(type) {
		case workflow.OutputEvent:
			if update, ok := e.Output.(*agent.ResponseUpdate); ok {
				if e.ExecutorID != lastExecutorID {
					lastExecutorID = e.ExecutorID
					demo.Assistantf("%s", e.ExecutorID)
				}
				if text := update.String(); text != "" {
					demo.Assistantf("%s", text)
				}
			}
		case workflow.ErrorEvent:
			demo.Panic(e.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", e.ExecutorID, e.Error)
		}
	}
}
