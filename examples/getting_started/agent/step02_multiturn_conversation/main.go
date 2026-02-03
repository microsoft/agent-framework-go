// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
)

var logger = demo.NewLogger(
	"Multi-Turn Conversation",
	"Demonstrates how to preserve conversation context with sessions.",
	"Model", "gpt-4o-mini",
)

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Config{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
		RunOptions:   []agentopt.RunOption{middleware.With(logger)}, // for logging agent interactions
	})

	ctx := context.Background()

	// Invoke the agent with a multi-turn conversation, where the context is preserved in the session object.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	resp, err := a.RunText("Tell me a joke about a pirate.", agentopt.Session(session)).Collect(ctx)
	demo.Response(resp, err)
	resp, err = a.RunText("Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Session(session)).Collect(ctx)
	demo.Response(resp, err)

	// Invoke the agent with a multi-turn conversation and streaming, where the context is preserved in the session object.
	session2, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}
	for update, err := range a.RunText("Tell me a joke about a pirate.", agentopt.Session(session2)).All(ctx) {
		demo.Response(update, err)
	}
	for update, err := range a.RunText("Now add some emojis to the joke and tell it in the voice of a pirate's parrot.", agentopt.Session(session2)).All(ctx) {
		demo.Response(update, err)
	}
}
