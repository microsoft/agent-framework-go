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
OpenAI Chat Agent Basic Example

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
	ag := openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-5-nano",
		APIKey:             os.Getenv("OPENAI_API_KEY"),
		SystemInstructions: "You are a helpful weather agent.",
		Opts: &agent.RunOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	nonStreamingExample(ag, "What's the weather like in Seattle?")
	streamingExample(ag, "What's the weather like in Portland, Oregon?")
}

func nonStreamingExample(ag *agent.Agent, query string) {
	ctx := context.Background()
	fmt.Println("=== Non-streaming Response Example ===")
	fmt.Println("User: ", query)
	fmt.Println("Result: ", must(ag.RunText(ctx, query)))
}

func streamingExample(ag *agent.Agent, query string) {
	ctx := context.Background()
	fmt.Println("=== Streaming Response Example ===")
	fmt.Println("User: ", query)
	stream := ag.RunStream(ctx, nil, nil, message.NewText(query))
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
