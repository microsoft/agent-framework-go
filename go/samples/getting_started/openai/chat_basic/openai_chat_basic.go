package main

import (
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
	resp, err := agent.RunText(a, query)
	if err != nil {
		panic(err)
	}
	fmt.Println("Result: ", resp)
}

func streamingExample(a agent.Agent, query string) {
	fmt.Println("=== Streaming Response Example ===")
	fmt.Println("User: ", query)
	for update, err := range agent.RunTextStream(a, query) {
		if err != nil {
			fmt.Print(err)
			break
		}
		fmt.Print(update)
	}
	fmt.Print("\n")
}
