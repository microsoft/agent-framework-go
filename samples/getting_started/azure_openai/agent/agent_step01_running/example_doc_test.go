package example_test

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
)

// Example_basicAgent demonstrates how to create and run a basic Azure OpenAI agent.
// This example shows the minimal setup needed to send a message and get a response.
func Example_basicAgent() {
	// Create an Azure OpenAI agent
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     "your-api-key",
		Endpoint:   "https://your-resource.openai.azure.com/",
		Model:      "gpt-4o",
		APIVersion: "2025-01-01-preview",
	}, &chatagent.Options{
		Instructions: "You are a helpful assistant.",
	})

	// Run a simple query (non-streaming)
	ctx := context.Background()
	response, err := agent.RunText(ctx, ag, "What is the capital of France?")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Response:", response)
}

// Example_streamingResponse demonstrates how to use streaming responses
// where tokens are received as they are generated.
func Example_streamingResponse() {
	// Create an Azure OpenAI agent
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     "your-api-key",
		Endpoint:   "https://your-resource.openai.azure.com/",
		Model:      "gpt-4o",
		APIVersion: "2025-01-01-preview",
	}, nil)

	ctx := context.Background()

	// Stream the response token by token
	fmt.Print("Streaming: ")
	for token, err := range agent.RunTextStream(ctx, ag, "Say hello") {
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
		fmt.Print(token)
	}
	fmt.Println()
}
