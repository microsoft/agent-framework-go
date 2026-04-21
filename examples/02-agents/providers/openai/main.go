// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/middleware"
	"github.com/openai/openai-go/v3"
)

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple agent run.",
	"Model", "gpt-4o-mini",
)

func main() {
	a := openaichatagent.New(
		openai.NewClient(),
		openaichatagent.Config{
			Model: "gpt-4o-mini",
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
