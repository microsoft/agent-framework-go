package main

import (
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/anthropic"
)

/*
Anthropic Chat Agent Basic Example

This sample demonstrates basic usage of anthropic.Agent for direct chat-based
interactions, showing both streaming and non-streaming responses.
*/

func main() {
	a := anthropic.NewChatAgent(anthropic.ClientConfig{
		Model:  "claude-sonnet-4-5",
		APIKey: os.Getenv("ANTHROPIC_API_KEY"),
	}, chatagent.Options{})

	nonStreamingExample(a, "What's the weather like in Seattle?")
	streamingExample(a, "What's the weather like in Portland, Oregon?")
}

func nonStreamingExample(a agent.Agent, query string) {
	fmt.Println("=== Non-streaming Response Example ===")
	fmt.Println("User: ", query)
	fmt.Println("Result: ", must(agent.RunText(context.Background(), a, query)))
}

func streamingExample(a agent.Agent, query string) {
	fmt.Println("=== Streaming Response Example ===")
	fmt.Println("User: ", query)
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
