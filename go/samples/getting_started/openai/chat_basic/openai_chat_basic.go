package main

import (
	"context"
	"fmt"
	"os"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Chat Agent Basic Example

This sample demonstrates basic usage of openai.Agent for direct chat-based
interactions, showing both streaming and non-streaming responses.
*/

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model:  "gpt-5-nano",
		APIKey: os.Getenv("OPENAI_API_KEY"),
	}, nil)

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
	stream := agent.RunTextStream(context.Background(), a, query)
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
