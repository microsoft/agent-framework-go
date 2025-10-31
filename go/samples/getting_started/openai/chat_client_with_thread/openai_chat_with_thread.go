package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/openai"
)

/*
OpenAI Chat Agent with Thread Management Example

This sample demonstrates thread management with OpenAI Chat Agent, showing
conversation threads and message history preservation across interactions.
*/

type weatherRequest struct {
	Location string `json:"location"`
}

type weatherResponse struct {
	Conditions string `json:"conditions"`
	HighTemp   int    `json:"high_temp"`
}

var weatherTool = agent.MustNewFuncTool(&agent.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, req weatherRequest) (weatherResponse, error) {
	return weatherResponse{
		Conditions: []string{"sunny", "cloudy", "rainy", "stormy"}[rand.Intn(4)],
		HighTemp:   rand.Intn(21) + 10,
	}, nil
})

func main() {
	fmt.Println("=== OpenAI Chat Agent Thread Management Examples ===")
	fmt.Println()

	exampleWithAutomaticThreadCreation()
	exampleWithThreadPersistence()
	exampleWithExistingThreadMessages()
}

// exampleWithAutomaticThreadCreation shows automatic thread creation (service-managed thread).
func exampleWithAutomaticThreadCreation() {
	fmt.Println("=== Automatic Thread Creation Example ===")

	client := openai.NewChatClient(openai.AgentConfig{
		Model: "gpt-4o-mini",
	})
	ag := agent.New(client, &agent.Config{
		SystemInstructions: "You are a helpful weather agent.",
	}, &agent.RunOptions{
		Tools: []agent.Tool{weatherTool},
	})

	ctx := context.Background()

	// First conversation - no thread provided, will be created automatically
	query1 := "What's the weather like in Seattle?"
	fmt.Printf("User: %s\n", query1)
	result1, err := ag.Run(ctx, nil, nil, agent.NewTextMessage(query1))
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Agent: %s\n", result1.Text())

	// Second conversation - still no thread provided, will create another new thread
	query2 := "What was the last city I asked about?"
	fmt.Printf("\nUser: %s\n", query2)
	result2, err := ag.Run(ctx, nil, nil, agent.NewTextMessage(query2))
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Agent: %s\n", result2.Text())
	fmt.Println("Note: Each call creates a separate thread, so the agent doesn't remember previous context.")
	fmt.Println()
}

// exampleWithThreadPersistence shows thread persistence across multiple conversations.
func exampleWithThreadPersistence() {
	fmt.Println("=== Thread Persistence Example ===")
	fmt.Println("Using the same thread across multiple conversations to maintain context.")
	fmt.Println()

	client := openai.NewChatClient(openai.AgentConfig{
		Model: "gpt-4o-mini",
	})
	ag := agent.New(client, &agent.Config{
		SystemInstructions: "You are a helpful weather agent.",
	}, &agent.RunOptions{
		Tools: []agent.Tool{weatherTool},
	})

	ctx := context.Background()

	// Create a new thread that will be reused
	thread := ag.NewThread()

	// First conversation
	query1 := "What's the weather like in Tokyo?"
	fmt.Printf("User: %s\n", query1)
	result1, err := ag.Run(ctx, thread, nil, agent.NewTextMessage(query1))
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Agent: %s\n", result1.Text())

	// Second conversation using the same thread - maintains context
	query2 := "How about London?"
	fmt.Printf("\nUser: %s\n", query2)
	result2, err := ag.Run(ctx, thread, nil, agent.NewTextMessage(query2))
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Agent: %s\n", result2.Text())

	// Third conversation - agent should remember both previous cities
	query3 := "Which of the cities I asked about has better weather?"
	fmt.Printf("\nUser: %s\n", query3)
	result3, err := ag.Run(ctx, thread, nil, agent.NewTextMessage(query3))
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Agent: %s\n", result3.Text())
	fmt.Println("Note: The agent remembers context from previous messages in the same thread.")
	fmt.Println()
}

// exampleWithExistingThreadMessages shows how to work with existing thread messages.
func exampleWithExistingThreadMessages() {
	fmt.Println("=== Existing Thread Messages Example ===")

	client := openai.NewChatClient(openai.AgentConfig{
		Model: "gpt-4o-mini",
	})
	ag := agent.New(client, &agent.Config{
		SystemInstructions: "You are a helpful weather agent.",
	}, &agent.RunOptions{
		Tools: []agent.Tool{weatherTool},
	})

	ctx := context.Background()

	// Start a conversation and build up message history
	thread := ag.NewThread()

	query1 := "What's the weather in Paris?"
	fmt.Printf("User: %s\n", query1)
	result1, err := ag.Run(ctx, thread, nil, agent.NewTextMessage(query1))
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Agent: %s\n", result1.Text())

	fmt.Println("\n--- Continuing with the same thread in a new agent instance ---")

	// Create a new agent instance but use the existing thread with its message history
	newClient := openai.NewChatClient(openai.AgentConfig{
		Model: "gpt-4o-mini",
	})
	newAgent := agent.New(newClient, &agent.Config{
		SystemInstructions: "You are a helpful weather agent.",
	}, &agent.RunOptions{
		Tools: []agent.Tool{weatherTool},
	})

	// Use the same thread object which contains the conversation history
	query2 := "What was the last city I asked about?"
	fmt.Printf("User: %s\n", query2)
	result2, err := newAgent.Run(ctx, thread, nil, agent.NewTextMessage(query2))
	if err != nil {
		log.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Agent: %s\n", result2.Text())
	fmt.Println("Note: The agent continues the conversation using the thread message history.")
	fmt.Println()
}
