// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/console"
	"github.com/microsoft/agent-framework-go/openai"
)

func main() {
	console.Welcome("Agent Basic Run", "A basic example of using an Agent with OpenAI backend.", "gpt-4o-mini")

	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Options{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
	})

	ctx := context.Background()

	// Invoke the agent and output the text result.
	console.AgentResponse(agent.RunText(ctx, a, "Tell me a joke about a pirate."))

	// Invoke the agent with streaming support.
	console.Agent()
	for update, err := range agent.RunTextStream(ctx, a, "Tell me a joke about a pirate.") {
		console.AgentStreamResponse(update, err)
	}
}
