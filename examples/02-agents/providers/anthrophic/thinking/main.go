// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/anthropicprovider"
)

// thinkingBudget is the number of tokens Anthropic may spend on extended
// thinking. It must be smaller than the request's max_tokens.
const thinkingBudget = 2048

var logger = demo.NewLogger(
	"Extended Thinking",
	"Demonstrates enabling Anthropic extended thinking and printing the reasoning.",
	"Model", "claude-sonnet-4-5",
	"Thinking budget", fmt.Sprintf("%d tokens", thinkingBudget),
)

func main() {
	// Create Anthropic agent.
	a := anthropicprovider.NewAgent(
		anthropic.NewClient(),
		anthropicprovider.AgentConfig{
			Model:        "claude-sonnet-4-5",
			Instructions: "You are a careful reasoner. Think the problem through, then reply with only the final answer.",
			Config: agent.Config{
				Name:        "Thinker",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	// Enable extended thinking for this run via the MessageNewParams per-run
	// escape hatch, giving the model a token budget to reason before answering.
	thinking := anthropicprovider.MessageNewParams(anthropic.MessageNewParams{
		Thinking: anthropic.ThinkingConfigParamOfEnabled(thinkingBudget),
	})

	// Invoke the agent with a prompt that elicits step-by-step reasoning.
	resp, err := a.RunText(context.Background(),
		"A bat and a ball cost $1.10 in total. The bat costs $1.00 more than the ball. How much does the ball cost?",
		thinking,
	).Collect()
	if err != nil {
		demo.Response(resp, err)
		return
	}

	// Print the reasoning (TextReasoningContent) separately from the final
	// answer (TextContent). Extended thinking is surfaced as reasoning content,
	// distinct from the model's output text. The logger middleware already
	// prints an "Assistant:" prefix for the run, so print the reasoning inline
	// without adding another prefix. Redacted thinking arrives as
	// TextReasoningContent with empty Text but non-empty ProtectedData; surface
	// it so the reasoning is not silently dropped.
	for c := range resp.Contents() {
		r, ok := c.(*message.TextReasoningContent)
		if !ok {
			continue
		}
		switch {
		case r.Text != "":
			fmt.Printf("Thinking: %s\n\n", r.Text)
		case r.ProtectedData != "":
			fmt.Print("Thinking: [redacted]\n\n")
		}
	}
	demo.Response(resp, nil)
}
