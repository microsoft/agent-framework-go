package main

import (
	"fmt"
	"os"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Chat Agent Basic Example

This sample demonstrates basic usage of openai.Agent for direct chat-based
interactions, showing both streaming and non-streaming responses.
*/

func main() {
	ag := openai.NewChatAgent(openai.ClientConfig{
		Model:  "gpt-5-nano",
		APIKey: os.Getenv("OPENAI_API_KEY"),
	}, nil)

	nonStreamingExample(ag, "What's the weather like in Seattle?")
	streamingExample(ag, "What's the weather like in Portland, Oregon?")
}

func nonStreamingExample(ag agent.Agent, query string) {
	fmt.Println("=== Non-streaming Response Example ===")
	fmt.Println("User: ", query)
	fmt.Println("Result: ", must(ag.Run(nil, message.NewText(query))))
}

func streamingExample(ag agent.Agent, query string) {
	fmt.Println("=== Streaming Response Example ===")
	fmt.Println("User: ", query)
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
