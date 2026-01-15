// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/anthropic"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
)

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple agent run.",
	"Model", "claude-sonnet-4-5",
)

func main() {
	// Create Anthropic agent
	a := anthropic.NewChatAgent(anthropic.ClientConfig{
		Model: "claude-sonnet-4-5",
	}, chatagent.Config{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
		Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
	})

	ctx := context.Background()

	// Invoke the agent and output the text result.
	demo.Response(agent.RunText(ctx, a, "Tell me a joke about a pirate."))

	// Invoke the agent with streaming support.
	for update, err := range agent.RunTextStream(ctx, a, "Tell me a joke about a pirate.") {
		demo.Response(update, err)
	}
}
