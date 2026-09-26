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

	// Attach the agent-mode context provider. With the default configuration the
	// agent starts in "plan" mode and can switch to "execute" via the mode_set
	// tool; the provider injects the current mode's instructions each turn. Modes
	// are session-backed, so the example runs inside a session.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a capable assistant that follows the current operating mode's guidance.",
			Config: agent.Config{
				Name:             "ModalAssistant",
				Middlewares:      []agent.Middleware{logger}, // for logging agent interactions
				ContextProviders: []agent.ContextProvider{agentmode.New(agentmode.Config{})},
			},
		},
	)

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText(ctx,
		"I want to organize a small study group. First plan the steps, then switch to execute mode and carry them out.",
		agent.WithSession(session)).Collect()
	demo.Response(resp, err)
}
