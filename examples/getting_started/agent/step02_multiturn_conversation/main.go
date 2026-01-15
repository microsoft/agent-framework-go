// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
)

var logger = demo.NewLogger(
	"Multi-Turn Conversation",
	"Demonstrates how to preserve conversation context with threads.",
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

	// Invoke the agent with a multi-turn conversation, where the context is preserved in the thread object.
	thread := a.NewThread(ctx)
	demo.Response(agent.RunText(ctx, a, "Tell me a joke about a pirate.", agentopt.Thread(thread)))
	demo.Response(agent.RunText(ctx, a, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Thread(thread)))

	// Invoke the agent with a multi-turn conversation and streaming, where the context is preserved in the thread object.
	thread2 := a.NewThread(ctx)
	for update, err := range agent.RunTextStream(ctx, a, "Tell me a joke about a pirate.", agentopt.Thread(thread2)) {
		demo.Response(update, err)
	}
	for update, err := range agent.RunTextStream(ctx, a, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Thread(thread2)) {
		demo.Response(update, err)
	}
}
