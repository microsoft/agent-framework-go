package main

import (
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Azure Chat Agent Basic Example

This sample demonstrates basic usage of openai.Agent for direct chat-based
interactions, showing both streaming and non-streaming responses.
*/

func main() {
	// Azure OpenAI configuration
	// You can also set these via environment variables:
	// - AZURE_OPENAI_API_KEY
	// - AZURE_OPENAI_ENDPOINT
	// - AZURE_OPENAI_DEPLOYMENT_NAME
	a := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     os.Getenv("AZURE_OPENAI_API_KEY"),         // or set directly
		Endpoint:   os.Getenv("AZURE_OPENAI_ENDPOINT"),        // e.g., "https://your-resource.openai.azure.com/"
		Model:      os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), // e.g., "gpt-4o"
		APIVersion: "2025-01-01-preview",                      // optional, uses default if not specified
	}, nil)

	// Example 1: Tool WILL be called (weather-related query)
	nonStreamingExample(a, "What's the weather like in Seattle?")

	// Example 2: Tool WILL be called (streaming)
	streamingExample(a, "What's the weather like in Portland?")

	// Example 3: Tool will NOT be called (general conversation)
	nonStreamingExample(a, "Hello! How are you today?")

	// Example 4: Tool will NOT be called (unrelated question)
	nonStreamingExample(a, "What is the capital of France?")

	// Example 5: Tool WILL be called (implicit weather request)
	nonStreamingExample(a, "Should I bring an umbrella in Boston today?")

}

func nonStreamingExample(a agent.Agent, query string) {
	fmt.Println("\n=== Non-streaming Response Example ===")
	fmt.Println("User: ", query)
	fmt.Println("Assistant: ", must(agent.RunText(context.Background(), a, query)))
}

func streamingExample(a agent.Agent, query string) {
	fmt.Println("\n=== Streaming Response Example ===")
	fmt.Println("User: ", query)
	fmt.Print("Assistant: ")
	for update, err := range agent.RunTextStream(context.Background(), a, query) {
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
