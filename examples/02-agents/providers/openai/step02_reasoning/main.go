// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

var model = cmp.Or(strings.TrimSpace(os.Getenv("OPENAI_CHAT_MODEL_NAME")), "gpt-5.4-mini")

var logger = demo.NewLogger(
	"OpenAI Reasoning",
	"Demonstrates non-streaming and streaming reasoning responses.",
	"Model", model,
)

func main() {
	ctx := context.Background()
	a := openaiprovider.NewResponsesAgent(
		openai.NewClient(),
		openaiprovider.AgentConfig{
			Model:        model,
			Instructions: "You are a careful problem solver. Think step by step before answering.",
			Config: agent.Config{
				Name:        "Reasoner",
				Middlewares: []agent.Middleware{logger},
			},
		},
	)
	reasoning := openaiprovider.ResponsesNewParams(responses.ResponseNewParams{
		Reasoning: shared.ReasoningParam{
			Effort:  shared.ReasoningEffortMedium,
			Summary: shared.ReasoningSummaryAuto,
		},
	})

	fmt.Println("1. Non-streaming:")
	resp, err := a.RunText(ctx,
		"Solve this problem step by step: If a train travels 60 miles per hour and needs to cover 180 miles, how long will the journey take? Show your reasoning.",
		reasoning,
	).Collect()
	if err != nil {
		demo.Panic(err)
	}
	demo.Response(resp, nil)
	for content := range resp.Contents() {
		if summary, ok := content.(*message.TextReasoningContent); ok && summary.Text != "" {
			fmt.Printf("[reasoning] %s\n", summary.Text)
		}
	}
	usage := resp.Usage()
	fmt.Printf("Token usage:\nInput: %d, Output: %d, Reasoning: %d\n\n", usage.InputTokenCount, usage.OutputTokenCount, usage.ReasoningTokenCount)

	fmt.Println("2. Streaming")
	for update, err := range a.RunText(ctx,
		"Explain the theory of relativity in simple terms.",
		agent.Stream(true),
		reasoning,
	) {
		if err != nil {
			demo.Panic(err)
		}
		for _, content := range update.Contents {
			switch content := content.(type) {
			case *message.TextReasoningContent:
				fmt.Print(content.Text)
			case *message.TextContent:
				fmt.Print(content.Text)
			}
		}
	}
	fmt.Println()
}
