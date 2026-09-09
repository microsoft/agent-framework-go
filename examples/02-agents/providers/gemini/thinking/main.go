// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/geminiprovider"
	"google.golang.org/genai"
)

var (
	apiKey = os.Getenv("GEMINI_API_KEY")
	model  = cmp.Or(os.Getenv("GEMINI_MODEL"), "gemini-2.5-flash")
)

var logger = demo.NewLogger(
	"Thinking",
	"Demonstrates a Gemini thinking agent exposing its reasoning summary.",
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

	// Create a Gemini agent backed by a thinking-capable model.
	a := geminiprovider.NewAgent(
		client,
		geminiprovider.AgentConfig{
			Model:        model,
			Instructions: "You are a careful reasoner. Explain your thinking step by step.",
			Config: agent.Config{
				Name:        "Thinker",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	// Enable thinking with thought summaries using the Gemini per-run config
	// escape hatch. Setting IncludeThoughts asks the model to return its
	// reasoning, which the provider maps to message.TextReasoningContent.
	thinking := geminiprovider.GenerateContentConfig(genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
		},
	})

	// Invoke the agent and collect the full response.
	resp, err := a.RunText(ctx,
		"A farmer has 17 sheep. All but 9 run away. How many are left? Think it through.",
		thinking,
	).Collect()

	// Print the final answer first, immediately after the logger middleware's
	// "Assistant:" prefix, then surface the reasoning summary (thought parts)
	// as a separate section below it.
	demo.Response(resp, err)
	if err != nil {
		return
	}

	for c := range resp.Contents() {
		if reasoning, ok := c.(*message.TextReasoningContent); ok && reasoning.Text != "" {
			fmt.Printf("[reasoning] %s\n\n", reasoning.Text)
		}
	}
}
