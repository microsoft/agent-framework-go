// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/anthropic"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
)

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple agent run.",
	"Model", "claude-sonnet-4-5",
)

func main() {
	// Create Anthropic agent
	a := anthropic.NewAgent(anthropic.Config{
		Model: "claude-sonnet-4-5",
		Agent: agent.Config{
			Instructions: "You are good at telling jokes.",
			Name:         "Joker",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		},
	})

	ctx := context.Background()

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)

	// Invoke the agent with streaming support.
	for update, err := range a.RunText(ctx, "Tell me a joke about a pirate.", agentopt.Stream(true)) {
		demo.Response(update, err)
	}
}
