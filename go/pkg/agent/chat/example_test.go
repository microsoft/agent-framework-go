// Copyright (c) Microsoft. All rights reserved.

package chat_test

import (
	"context"
	"fmt"
	"iter"
	"log"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	"github.com/microsoft/agent-framework/go/pkg/agent/chat"
)

// mockChatClient is a simple mock implementation for demonstration.
type mockChatClient struct{}

// Complete implements the ChatClient interface.
func (m *mockChatClient) Complete(ctx context.Context, options *chat.Options, messages ...*agent.Message) (*chat.Response, error) {
	return &chat.Response{
		Message:      agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "Hello! This is a mock response."}),
		FinishReason: agent.FinishReasonStop,
		Usage: &agent.UsageDetails{
			InputTokenCount:  10,
			OutputTokenCount: 8,
			TotalTokenCount:  18,
		},
		ModelID: "mock-model",
	}, nil
}

// CompleteStream implements the ChatClient interface for streaming.
func (m *mockChatClient) CompleteStream(ctx context.Context, options *chat.Options, messages ...*agent.Message) iter.Seq2[*chat.ResponseUpdate, error] {
	resp := []*chat.ResponseUpdate{
		{
			Delta:        agent.NewMessage(agent.RoleAssistant, &agent.TextContent{Text: "Hello! This is a streaming mock response."}),
			FinishReason: agent.FinishReasonStop,
			Usage: &agent.UsageDetails{
				InputTokenCount:  10,
				OutputTokenCount: 9,
				TotalTokenCount:  19,
			},
			ModelID: "mock-model",
		},
	}
	return func(yield func(*chat.ResponseUpdate, error) bool) {
		for _, r := range resp {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func Example_customAgent() {
	ctx := context.Background()

	// Create a mock chat client
	chatClient := &mockChatClient{}

	// Create an agent
	myAgent := chat.New(chat.Config{
		Name:         "ExampleAgent",
		Instructions: "You are a helpful assistant.",
		Client:       chatClient,
	})

	// Create a message
	userMessage := agent.NewTextMessage("Hello, how are you?")

	// Run the agent
	response, err := myAgent.Run(ctx, nil, nil, userMessage)
	if err != nil {
		log.Fatalf("Error running agent: %v", err)
	}

	// Print the response
	fmt.Printf("\nAgent Response: %s\n", response.Message.Text())
	fmt.Printf("Model ID: %s\n", response.ModelID)
	fmt.Printf("Usage: %d input + %d output = %d total tokens\n",
		response.Usage.InputTokenCount,
		response.Usage.OutputTokenCount,
		response.Usage.TotalTokenCount)

	// Example with streaming
	fmt.Println("\n--- Streaming Example ---")
	for update := range agent.RunStream(ctx, myAgent, nil, nil, userMessage) {
		if update.Delta != nil {
			for _, content := range update.Delta.Contents {
				if textContent, ok := content.(*agent.TextContent); ok {
					fmt.Printf("Streaming: %s\n", textContent.Text)
				}
			}
		}
		if update.FinishReason != "" {
			fmt.Printf("Finished: %s\n", update.FinishReason)
		}
	}
	fmt.Println("\nStreaming completed")

	// Output:
	// Agent Response: Hello! This is a mock response.
	// Model ID: mock-model
	// Usage: 10 prompt + 8 completion = 18 total tokens
	//
	// --- Streaming Example ---
	// Streaming: Hello! This is a streaming mock response.
	// Finished: stop
	//
	// Streaming completed
}
