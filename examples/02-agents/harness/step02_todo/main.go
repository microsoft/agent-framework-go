// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Todo Harness",
	"This sample shows how the todo harness gives an agent tools to plan and track a "+
		"multi-step task. The current todo list is injected on each turn, and the agent "+
		"adds, completes, and removes items as it works.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	// Attach the todo context provider: it exposes todo tools to the agent and
	// injects the current todo list summary on each invocation. The todos are
	// session-backed, so the example runs inside a session.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a planful assistant. For multi-step requests, use the todo tools to " +
				"break the work into items, then complete them one by one, marking each item complete " +
				"when finished.",
			Config: agent.Config{
				Name:             "Planner",
				Middlewares:      []agent.Middleware{logger}, // for logging agent interactions
				ContextProviders: []agent.ContextProvider{todo.New(nil)},
			},
		},
	)

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText(ctx,
		"Plan a simple three-course dinner: choose a starter, a main, and a dessert. "+
			"Track each course as a todo item and complete them one by one.",
		agent.WithSession(session)).Collect()
	demo.Response(resp, err)
}
