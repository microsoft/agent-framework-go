package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

/*
OpenAI Azure Chat Agent Basic Example

This sample demonstrates basic usage of openai.Agent for direct chat-based
interactions, showing both streaming and non-streaming responses.
*/

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
	return fmt.Sprintf("The weather in %s is %s with a high of %d°C.", location, conditions[rand.Intn(len(conditions))], rand.Intn(21)+10), nil
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

	// Add this debug code:
	if ag.Config.Opts != nil && len(ag.Config.Opts.Tools) > 0 {
		fmt.Println("📋 Registered tools:")
		for _, t := range ag.Config.Opts.Tools {
			name, desc := t.ToolInfo()
			fmt.Printf("  - %s: %s\n", name, desc)
		}
		fmt.Println()
	}

	// Example 1: Tool WILL be called (weather-related query)
	nonStreamingExample(ag, "What's the weather like in Seattle?")

	// Example 2: Tool WILL be called (streaming)
	streamingExample(ag, "What's the weather like in Portland?")

	// Example 3: Tool will NOT be called (general conversation)
	nonStreamingExample(ag, "Hello! How are you today?")

	// Example 4: Tool will NOT be called (unrelated question)
	nonStreamingExample(ag, "What is the capital of France?")

	// Example 5: Tool WILL be called (implicit weather request)
	nonStreamingExample(ag, "Should I bring an umbrella in Boston today?")

}

func nonStreamingExample(ag *agent.Agent, query string) {
	fmt.Println("\n=== Non-streaming Response Example ===")
	fmt.Println("User: ", query)
	fmt.Println("Assistant: ", must(ag.RunText(nil, query)))
}

func streamingExample(ag *agent.Agent, query string) {
	fmt.Println("\n=== Streaming Response Example ===")
	fmt.Println("User: ", query)
	fmt.Print("Assistant: ")
	stream := ag.RunStream(nil, message.NewText(query))
	for update, err := range stream {
		if err != nil {
			fmt.Print(err)
			break
		}
		fmt.Print(update)
	}
	fmt.Print("\n")
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
