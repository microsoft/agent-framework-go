// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

var logger = demo.NewLogger(
	"Multi-Turn Conversation",
	"Demonstrates how to preserve conversation context with sessions.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	// Create Microsoft Foundry agent.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are good at telling jokes.",
			Config: agent.Config{
				Name:        "Joker",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()

	// Run a multi-turn conversation using the same session.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)
	resp, err = a.RunText(ctx, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agent.WithSession(session)).Collect()
	demo.Response(resp, err)

	// Run a streamed multi-turn conversation using the same session.
	session2, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for update, err := range a.RunText(ctx, "Tell me a joke about a pirate.", agent.WithSession(session2), agent.Stream(true)) {
		demo.Response(update, err)
	}
	for update, err := range a.RunText(ctx, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agent.WithSession(session2), agent.Stream(true)) {
		demo.Response(update, err)
	}
}
