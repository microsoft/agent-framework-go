// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
)

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple agent run.",
	"Model", "gpt-4o-mini",
)

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
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
