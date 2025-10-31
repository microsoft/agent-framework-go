package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Chat Agent Basic Example

This sample demonstrates basic usage of openai.Agent for direct chat-based
interactions, showing both streaming and non-streaming responses.
*/

type weatherRequest struct {
	Location string `json:"location"`
}

type weatherResponse struct {
	Conditions string `json:"conditions"`
	HighTemp   int    `json:"high_temp"`
}

var weatherTool = agent.MustNewFuncTool(&agent.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, req weatherRequest) (weatherResponse, error) {
	return weatherResponse{
		Conditions: []string{"sunny", "cloudy", "rainy", "stormy"}[rand.Intn(4)],
		HighTemp:   rand.Intn(21) + 10,
	}, nil
})

func main() {
	// OpenAI configuration
	// Set your API key via environment variable: export OPENAI_API_KEY=your-key-here
	// Or get one from: https://platform.openai.com/account/api-keys
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required. Get your key from https://platform.openai.com/account/api-keys")
	}

	client := openai.NewChatClient(openai.AgentConfig{
		Model:  "gpt-5-nano",
		APIKey: apiKey,
	})

	ag := agent.New(client, &agent.Config{
		SystemInstructions: "You are a helpful weather agent.",
	}, &agent.RunOptions{
		Tools: []agent.Tool{weatherTool},
	})

	nonStreamingExample(ag, "What's the weather like in Seattle?")
	streamingExample(ag, "What's the weather like in Portland, Oregon?")
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
