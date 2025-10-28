package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	"github.com/microsoft/agent-framework/go/pkg/openai"
)

func main() {
	// Azure OpenAI configuration
	// You can also set these via environment variables:
	// - AZURE_OPENAI_API_KEY
	// - AZURE_OPENAI_ENDPOINT
	// - AZURE_OPENAI_DEPLOYMENT_NAME
	client := openai.NewAzureChatClient(openai.AzureChatClientConfig{
		APIKey:         os.Getenv("AZURE_OPENAI_API_KEY"),         // or set directly
		Endpoint:       os.Getenv("AZURE_OPENAI_ENDPOINT"),        // e.g., "https://your-resource.openai.azure.com/"
		DeploymentName: os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), // e.g., "gpt-4o"
		APIVersion:     "2025-01-01-preview",                      // optional, uses default if not specified
	})

	ag := client.NewAgent("You are a helpful weather agent.")

	nonStreamingExample(ag, "What's the weather like in Seattle?")
	streamingExample(ag, "What's the weather like in Portland?")
}

func nonStreamingExample(ag agent.Agent, query string) {
	ctx := context.Background()
	log.Printf("=== Non-streaming Response Example ===\n")
	log.Printf("User: %s\n", query)
	resp, err := ag.Run(ctx, nil, nil, agent.NewTextMessage(query))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Result: %s\n", resp.Message.Text())
}

func streamingExample(ag agent.Agent, query string) {
	ctx := context.Background()
	log.Printf("=== Streaming Response Example ===\n")
	log.Printf("User: %s\n", query)
	stream := agent.RunStream(ctx, ag, nil, nil, agent.NewTextMessage(query))
	for update := range stream {
		if update.Delta != nil {
			fmt.Print(update.Delta.Text())
		}
	}
	fmt.Print("\n")
}
