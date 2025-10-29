//go:build ignore

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	"github.com/microsoft/agent-framework/go/pkg/agent/chat"
	"github.com/microsoft/agent-framework/go/pkg/openai"
)

/*
OpenAI Chat Client with Web Search Example

This sample demonstrates using HostedWebSearchTool with OpenAI Chat Client
for real-time information retrieval and current data access.
*/

func main() {
	client := openai.NewChatClient(openai.ChatClientConfig{
		Model: "gpt-4o-search-preview",
	})
	ag := client.NewAgent(&chat.Config{
		Instructions: "You are a helpful weather agent.",
		Options: &chat.Options{
			Tools: []agent.Tool{&agent.HostedWebSearchTool{
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

func nonStreamingExample(ag agent.Agent, query string) {
	ctx := context.Background()
	fmt.Printf("=== Non-streaming Response Example ===\n")
	fmt.Printf("User: %s\n", query)
	resp, err := agent.RunText(ctx, ag, query)
	if err != nil {
		fmt.Print(err)
		return
	}
	fmt.Printf("Result: %s\n", resp.Message.Text())
}

func streamingExample(ag agent.Agent, query string) {
	ctx := context.Background()
	fmt.Printf("=== Streaming Response Example ===\n")
	fmt.Printf("User: %s\n", query)
	stream := agent.RunStream(ctx, ag, nil, nil, agent.NewTextMessage(query))
	for update, err := range stream {
		if err != nil {
			fmt.Print(err)
			return
		}
		fmt.Print(update.Delta.Text())
	}
	fmt.Print("\n")
}
