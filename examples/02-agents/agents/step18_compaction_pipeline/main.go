// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to use a compaction context provider with a compaction pipeline.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Compaction Pipeline",
	"Demonstrates how to chain compaction strategies for long-running conversations.",
	"Model", demo.FoundryModel,
)

var lookupPriceTool = functool.MustNew(functool.Config{
	Name:        "lookup_price",
	Description: "Look up the current price of a product by name. The product name should be either LAPTOP, KEYBOARD, or MOUSE.",
}, lookupPrice)

func main() {
	token := demo.FoundryTokenCredential()

	// Create a separate summarizer agent. In production, this could use a smaller or cheaper model.
	summarizerAgent := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Config: agent.Config{
				Name: "ConversationSummarizer",
			},
		},
	)
	summarizer := compaction.SummarizerFunc(func(ctx context.Context, messages []*message.Message) (string, error) {
		resp, err := summarizerAgent.Run(ctx, messages).Collect()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.String()), nil
	})

	// Configure the compaction pipeline from least aggressive to most aggressive.
	compactionPipeline := &compaction.PipelineStrategy{
		Strategies: []compaction.Strategy{
			// 1. Gentle: collapse old tool-call groups into short summaries.
			&compaction.ToolResultStrategy{
				Trigger:                compaction.MessagesExceed(7),
				MinimumPreservedGroups: 4,
			},
			// 2. Moderate: use an LLM to summarize older conversation spans into a concise message.
			&compaction.SummarizationStrategy{
				Trigger:                compaction.TokensExceed(0x500),
				Summarizer:             summarizer,
				MinimumPreservedGroups: 4,
			},
			// 3. Aggressive: keep only the last N user turns and their responses.
			&compaction.SlidingWindowStrategy{
				Trigger:               compaction.TurnsExceed(4),
				MinimumPreservedTurns: 4,
			},
			// 4. Emergency: drop oldest groups until under the token budget.
			&compaction.TruncationStrategy{
				Trigger:                compaction.TokensExceed(0x8000),
				MinimumPreservedGroups: 8,
			},
		},
	}

	// Create Microsoft Foundry agent with the compaction pipeline and a price-lookup tool.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: `You are a helpful, but long-winded, shopping assistant.
Help the user look up prices and compare products.
When responding, be extra descriptive and use as many words as possible without sounding ridiculous.`,
			Config: agent.Config{
				Name: "ShoppingAssistant",
				ContextProviders: []agent.ContextProvider{
					compaction.NewContextProvider(compaction.ContextProviderConfig{
						Strategy: compactionPipeline,
						Logger:   slog.New(logger),
					}),
				},
				Tools: []tool.Tool{lookupPriceTool},
				Middlewares: []agent.Middleware{
					logger, // for logging agent interactions
				},
			},
		},
	)

	ctx := context.Background()
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panic(err)
	}

	prompts := []string{
		"What's the price of a laptop?",
		"How about a keyboard?",
		"And a mouse?",
		"Which product is the cheapest?",
		"Can you compare the laptop and the keyboard for me?",
		"What was the first product I asked about?",
		"Thank you!",
	}

	for _, prompt := range prompts {
		resp, err := a.RunText(ctx, prompt, agent.WithSession(session)).Collect()
		demo.Response(resp, err)
	}
}

func lookupPrice(_ context.Context, productName string) (string, error) {
	switch strings.ToUpper(productName) {
	case "LAPTOP":
		return "The laptop costs $999.99.", nil
	case "KEYBOARD":
		return "The keyboard costs $79.99.", nil
	case "MOUSE":
		return "The mouse costs $29.99.", nil
	default:
		return fmt.Sprintf("Sorry, I don't have pricing for %q.", productName), nil
	}
}
