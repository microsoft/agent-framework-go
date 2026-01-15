// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple agent run.",
	"Model", deployment,
)

func main() {
	// Create Azure OpenAI agent with weather tool
	a := openai.NewChatAgentAzure(openai.ClientConfig{
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
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
