// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"iter"
	"log"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	"github.com/microsoft/agent-framework/go/pkg/client"
	"github.com/microsoft/agent-framework/go/pkg/message"
	"github.com/microsoft/agent-framework/go/pkg/types"
)

// mockChatClient is a simple mock implementation for demonstration.
type mockChatClient struct{}

// Complete implements the ChatClient interface.
func (m *mockChatClient) Complete(ctx context.Context, options *client.ChatOptions, messages ...*message.ChatMessage) (*message.ChatResponse, error) {
	return &message.ChatResponse{
		Message:      message.NewChatMessage(types.RoleAssistant, "Hello! This is a mock response."),
		FinishReason: types.FinishReasonStop,
		Usage: &types.UsageDetails{
			PromptTokens:     10,
			CompletionTokens: 8,
			TotalTokens:      18,
		},
		ModelID: "mock-model",
	}, nil
}

// CompleteStream implements the ChatClient interface for streaming.
func (m *mockChatClient) CompleteStream(ctx context.Context, options *client.ChatOptions, messages ...*message.ChatMessage) iter.Seq2[*message.ChatResponseUpdate, error] {
	resp := []*message.ChatResponseUpdate{
		{
			Delta:        message.NewChatMessage(types.RoleAssistant, "Hello! This is a streaming mock response."),
			FinishReason: types.FinishReasonStop,
			Usage: &types.UsageDetails{
				PromptTokens:     10,
				CompletionTokens: 9,
				TotalTokens:      19,
			},
			ModelID: "mock-model",
		},
	}
	return func(yield func(*message.ChatResponseUpdate, error) bool) {
		for _, r := range resp {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func main() {
	ctx := context.Background()

	// Create a mock chat client
	chatClient := &mockChatClient{}

	// Create an agent
	myAgent := agent.NewChatAgent(agent.ChatAgentConfig{
		Name:         "ExampleAgent",
		Instructions: "You are a helpful assistant.",
		ChatClient:   chatClient,
	})

	fmt.Printf("Created agent: %s (ID: %s)\n", myAgent.Name(), myAgent.ID())

	// Create a message
	userMessage := message.NewChatMessage(types.RoleUser, "Hello, how are you?")

	// Run the agent
	response, err := myAgent.Run(ctx, nil, nil, userMessage)
	if err != nil {
		log.Fatalf("Error running agent: %v", err)
	}

	// Print the response
	fmt.Printf("\nAgent Response:\n%s\n", response.Text())
	fmt.Printf("Model ID: %s\n", response.ModelID)
	fmt.Printf("Usage: %d prompt + %d completion = %d total tokens\n",
		response.Usage.PromptTokens,
		response.Usage.CompletionTokens,
		response.Usage.TotalTokens)

	// Example with streaming
	fmt.Println("\n--- Streaming Example ---")
	for update := range agent.RunStream(ctx, myAgent, nil, nil, userMessage) {
		if update.Delta != nil {
			for _, content := range update.Delta.Contents {
				if textContent, ok := content.(*message.TextContent); ok {
					fmt.Printf("Streaming: %s\n", textContent.Text)
				}
			}
		}
		if update.FinishReason != "" {
			fmt.Printf("Finished: %s\n", update.FinishReason)
		}
	}
	fmt.Println("\nStreaming completed")
}
