package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/websearchtool"
)

/*
OpenAI Chat Agent with Web Search Example

This sample demonstrates using HostedWebSearchTool with OpenAI Chat Agent
for real-time information retrieval and current data access.
*/

func main() {
	ag := openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-4o-search-preview",
		SystemInstructions: "You are a helpful weather agent.",
		Opts: &agent.RunOptions{
			Tools: []tool.Tool{&websearchtool.HostedWebSearch{
				AdditionalProperties: map[string]any{
					"user_location": map[string]string{
						"country": "US",
						"city":    "Seattle",
					},
				},
			}},
		},
	})

	const message = "What is the current weather? Do not ask for my current location."
	if true {
		nonStreamingExample(ag, message)
	} else {
		streamingExample(ag, message)
	}
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
