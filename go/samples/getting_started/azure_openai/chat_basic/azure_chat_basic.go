package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Azure Chat Agent Basic Example

This sample demonstrates basic usage of openai.Agent for direct chat-based
interactions, showing both streaming and non-streaming responses.
*/

var weatherTool = agent.MustNewFunc(
	"weather", "Get the current weather for a given location",
	[]agent.FuncParameter{
		{Name: "location", Description: "The location to get the weather for"},
	},
	func(location string) string {
		conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
		return fmt.Sprintf("The weather in %s is %s with a high of %d°C.", location, conditions[rand.Intn(4)], rand.Intn(21)+10)
	},
)

func main() {
	// Azure OpenAI configuration
	// You can also set these via environment variables:
	// - AZURE_OPENAI_API_KEY
	// - AZURE_OPENAI_ENDPOINT
	// - AZURE_OPENAI_DEPLOYMENT_NAME
	client := openai.NewAzureChatClient(openai.AgentConfig{
		APIKey:     os.Getenv("AZURE_OPENAI_API_KEY"),         // or set directly
		Endpoint:   os.Getenv("AZURE_OPENAI_ENDPOINT"),        // e.g., "https://your-resource.openai.azure.com/"
		Model:      os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), // e.g., "gpt-4o"
		APIVersion: "2025-01-01-preview",                      // optional, uses default if not specified
	})

	ag := agent.New(client, &agent.Config{
		SystemInstructions: "You are a helpful weather agent.",
	}, &agent.RunOptions{
		Tools: []agent.Tool{weatherTool},
	})

	nonStreamingExample(ag, "What's the weather like in Seattle?")
	streamingExample(ag, "What's the weather like in Portland?")
}

func nonStreamingExample(ag *agent.Agent, query string) {
	ctx := context.Background()
	fmt.Printf("=== Non-streaming Response Example ===\n")
	fmt.Printf("User: %s\n", query)
	resp, err := ag.RunText(ctx, query)
	if err != nil {
		fmt.Print(err)
		return
	}
	fmt.Printf("Result: %s\n", resp.Text())
}

func streamingExample(ag *agent.Agent, query string) {
	ctx := context.Background()
	fmt.Printf("=== Streaming Response Example ===\n")
	fmt.Printf("User: %s\n", query)
	stream := ag.RunStream(ctx, nil, nil, agent.NewTextMessage(query))
	for update, err := range stream {
		if err != nil {
			fmt.Print(err)
			return
		}
		fmt.Print(update.Text())
	}
	fmt.Print("\n")
}
