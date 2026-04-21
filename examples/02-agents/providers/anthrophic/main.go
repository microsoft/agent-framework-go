// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/anthropicagent"
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
	a := anthropicagent.New(
		anthropic.NewClient(),
		anthropicagent.Config{
			Model: "claude-sonnet-4-5",
			Config: agent.Config{
				Instructions: "You are good at telling jokes.",
				Name:         "Joker",
				Middlewares:  []middleware.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	// Invoke the agent and output the text result.
	resp, err := a.RunText(context.Background(), "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
