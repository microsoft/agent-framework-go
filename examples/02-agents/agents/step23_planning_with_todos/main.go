// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to combine the agentmode and todo harness context providers on a single agent.
//
// The agentmode provider tracks the agent's operating mode (e.g. "plan" vs "execute") in the session
// state and exposes mode_get/mode_set tools. The todo provider gives the agent a persistent todo list
// with todos_add, todos_complete, todos_remove, todos_get_remaining, and todos_get_all tools. Used
// together they let an agent enter plan mode, break a request into trackable todo items, switch to
// execute mode, and complete those items while checking what remains. Both providers persist their
// state in the agent session, so the plan and progress survive across turns.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/agentmode"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Planning With Todos",
	"Demonstrates combining the agentmode and todo context providers to plan then execute a multi-step task.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	// The agent mode provider tracks whether the agent is planning or executing.
	// It defaults to "plan" and "execute" modes and injects mode-specific instructions.
	modeProvider := agentmode.New(agentmode.Config{
		DefaultMode: "plan",
	})

	// The todo provider gives the agent a persistent todo list it can add to, query, and complete.
	todoProvider := todo.New(&todo.Options{})

	// Create a Microsoft Foundry agent with both context providers attached.
	// Providers are invoked in sequence, each contributing tools, instructions, and context messages.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: `You are a diligent engineering assistant that plans before acting.
Start every substantive request in plan mode: analyze the work, then record concrete todo items with todos_add.
Only switch to execute mode with mode_set when the user explicitly asks you to.
As you finish each item, mark it done with todos_complete (include a reason describing how it was completed), and use todos_get_remaining to see what is left.`,
			DisableStoreOutput: true,
			Config: agent.Config{
				Name: "PlanningAssistant",
				ContextProviders: []agent.ContextProvider{
					modeProvider,
					todoProvider,
				},
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()

	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	prompts := []string{
		"I want to add a health-check endpoint to our web service. Plan the work first: enter plan mode and record the todo items needed.",
		"The plan looks good. Switch to execute mode and start working through the todo items, marking each one complete as you finish it.",
		"What work is still remaining on the todo list?",
	}
	for _, prompt := range prompts {
		resp, err := a.RunText(ctx, prompt, agent.WithSession(session)).Collect()
		demo.Response(resp, err)
	}
}
