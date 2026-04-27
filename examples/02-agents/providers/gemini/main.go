// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/geminiagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"google.golang.org/genai"
)

var (
	apiKey = os.Getenv("GEMINI_API_KEY")
	model  = cmp.Or(os.Getenv("GEMINI_MODEL"), "gemini-2.5-flash")
)

var logger = demo.NewLogger(
	"Basic Run",
	"Demonstrates a simple Gemini agent run.",
	"Model", model,
)

func main() {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		demo.Panicf("failed to create Gemini client: %v", err)
	}

	// Create Gemini agent
	a := geminiagent.New(
		client,
		geminiagent.Config{
			Model: model,
			Config: agent.Config{
				Instructions: "You are good at telling jokes.",
				Name:         "Joker",
				Middlewares:  []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
