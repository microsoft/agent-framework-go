// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
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
		RunOptions:   []agentopt.RunOption{middleware.With(logger)}, // for logging agent interactions
	})

	ctx := context.Background()

	// Invoke the agent and output the text result.
	resp, err := a.RunText("Tell me a joke about a pirate.").Collect(ctx)
	demo.Response(resp, err)

	// Invoke the agent with streaming support.
	for update, err := range a.RunText("Tell me a joke about a pirate.").All(ctx) {
		demo.Response(update, err)
	}
}
