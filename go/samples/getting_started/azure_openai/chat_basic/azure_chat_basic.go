package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/openai"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

/*
OpenAI Azure Chat Agent Basic Example

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

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, req weatherRequest) (weatherResponse, error) {
	return weatherResponse{
		Conditions: []string{"sunny", "cloudy", "rainy", "stormy"}[rand.Intn(4)],
		HighTemp:   rand.Intn(21) + 10,
	}, nil
})

func main() {
	// Azure OpenAI configuration
	// You can also set these via environment variables:
	// - AZURE_OPENAI_API_KEY
	// - AZURE_OPENAI_ENDPOINT
	// - AZURE_OPENAI_DEPLOYMENT_NAME
	ag := openai.NewChatAgentAzure(openai.AgentConfig{
		APIKey:             os.Getenv("AZURE_OPENAI_API_KEY"),         // or set directly
		Endpoint:           os.Getenv("AZURE_OPENAI_ENDPOINT"),        // e.g., "https://your-resource.openai.azure.com/"
		Model:              os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), // e.g., "gpt-4o"
		APIVersion:         "2025-01-01-preview",                      // optional, uses default if not specified
		SystemInstructions: "You are a helpful weather agent.",
		Opts: &agent.RunOptions{
			Tools: []tool.Tool{weatherTool},
		},
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
