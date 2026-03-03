// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
)

var logger = demo.NewLogger(
	"Multi-Turn Conversation",
	"Demonstrates how to preserve conversation context with sessions.",
	"Model", "gpt-4o-mini",
)

func main() {
	a := openaichat.NewAgent(openaichat.Config{
		Model: "gpt-4o-mini",
		Agent: agent.Config{
			Instructions: "You are good at telling jokes.",
			Name:         "Joker",
			Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
		},
	})

	ctx := context.Background()

	// Invoke the agent with a multi-turn conversation, where the context is preserved in the session object.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.", agentopt.Session(session)).Collect()
	demo.Response(resp, err)
	resp, err = a.RunText(ctx, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Session(session)).Collect()
	demo.Response(resp, err)

	// Invoke the agent with a multi-turn conversation and streaming, where the context is preserved in the session object.
	session2, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for update, err := range a.RunText(ctx, "Tell me a joke about a pirate.", agentopt.Session(session2), agentopt.Stream(true)) {
		demo.Response(update, err)
	}
	for update, err := range a.RunText(ctx, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Session(session2), agentopt.Stream(true)) {
		demo.Response(update, err)
	}
}
