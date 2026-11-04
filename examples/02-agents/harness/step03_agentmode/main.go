// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/agentmode"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Agent Mode Harness",
	"This sample shows how the agent-mode harness gives an agent a set of operating "+
		"modes (plan / execute by default) plus mode_set/mode_get tools, and injects "+
		"mode-specific instructions so the agent adjusts its behavior per mode.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	// The agent-mode context provider injects the current mode's instructions at
	// the start of each run. Because instructions are rebuilt per run, a single
	// run only ever sees one mode; to demonstrate both, the example issues one
	// run per mode and switches the session's mode in between. Keep a reference
	// to the provider so we can drive that switch.
	modeProvider := agentmode.New(agentmode.Config{})

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a capable assistant that follows the current operating mode's guidance.",
			Config: agent.Config{
				Name:             "ModeAssistant",
				Middlewares:      []agent.Middleware{logger}, // for logging agent interactions
				ContextProviders: []agent.ContextProvider{modeProvider},
			},
		},
	)

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	// Run 1: the session starts in "plan" mode, so the provider injects the
	// plan-mode instructions and the agent plans the work.
	planResp, err := a.RunText(ctx,
		"I want to organize a small study group. Plan the steps before doing anything.",
		agent.WithSession(session)).Collect()
	demo.Response(planResp, err)

	// Switch the session to "execute" mode. The next run rebuilds the injected
	// instructions from this mode, so the agent now carries out the plan.
	if err := modeProvider.SetMode(session, "execute", false); err != nil {
		demo.Panic(err)
	}

	// Run 2: now in "execute" mode, the provider injects the execute-mode
	// instructions and the agent carries out the planned steps.
	execResp, err := a.RunText(ctx,
		"Now execute the plan you just made.",
		agent.WithSession(session)).Collect()
	demo.Response(execResp, err)
}
