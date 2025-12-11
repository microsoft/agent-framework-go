package example_test

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
)

// Example_multiTurnConversation demonstrates how to maintain conversation
// context across multiple messages using a thread.
func Example_multiTurnConversation() {
	// Create an Azure OpenAI agent
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     "your-api-key",
		Endpoint:   "https://your-resource.openai.azure.com/",
		Model:      "gpt-4o",
		APIVersion: "2025-01-01-preview",
	}, &chatagent.Options{
		Instructions: "You are a helpful assistant.",
	})

	ctx := context.Background()

	// Create a thread to preserve conversation context
	thread := ag.NewThread()

	// First message
	response1, _ := agent.RunText(ctx, ag, "My name is Alice.", agent.WithThread(thread))
	fmt.Println("Assistant:", response1)

	// Second message - agent remembers the context
	response2, _ := agent.RunText(ctx, ag, "What is my name?", agent.WithThread(thread))
	fmt.Println("Assistant:", response2)
}

// Example_newThreadPerConversation demonstrates that each thread
// maintains its own separate conversation context.
func Example_newThreadPerConversation() {
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     "your-api-key",
		Endpoint:   "https://your-resource.openai.azure.com/",
		Model:      "gpt-4o",
		APIVersion: "2025-01-01-preview",
	}, nil)

	ctx := context.Background()

	// Conversation 1
	thread1 := ag.NewThread()
	agent.RunText(ctx, ag, "Remember: the secret word is 'banana'.", agent.WithThread(thread1))

	// Conversation 2 - separate thread, no shared context
	thread2 := ag.NewThread()
	response, _ := agent.RunText(ctx, ag, "What is the secret word?", agent.WithThread(thread2))
	fmt.Println("Response:", response)
}
