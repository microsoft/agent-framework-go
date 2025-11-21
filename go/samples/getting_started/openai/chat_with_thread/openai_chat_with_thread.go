package main

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/functool"
)

/*
OpenAI Chat Agent with Thread Management Example

This sample demonstrates thread management with OpenAI Chat Agent, showing
conversation threads and message history preservation across interactions.
*/

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
	return fmt.Sprintf("The weather in %s is %s with a high of %d°C.", location, conditions[rand.Intn(len(conditions))], rand.Intn(21)+10), nil
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

	ag := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		Instructions: "You are a helpful weather agent.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	// First conversation - no thread provided, will be created automatically
	query1 := "What's the weather like in Seattle?"
	fmt.Println("User: ", query1)
	fmt.Println("Agent: ", must(ag.Run(nil, message.NewText(query1))))

	// Second conversation - still no thread provided, will create another new thread
	query2 := "What was the last city I asked about?"
	fmt.Println("User: ", query2)
	fmt.Println("Agent: ", must(ag.Run(nil, message.NewText(query2))))
	fmt.Println("Note: Each call creates a separate thread, so the agent doesn't remember previous context.")
	fmt.Println()
}

// exampleWithThreadPersistence shows thread persistence across multiple conversations.
func exampleWithThreadPersistence() {
	fmt.Println("=== Thread Persistence Example ===")
	fmt.Println("Using the same thread across multiple conversations to maintain context.")
	fmt.Println()

	ag := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	// Create a new thread that will be reused
	thread := ag.NewThread()

	// First conversation
	query1 := "What's the weather like in Tokyo?"
	fmt.Println("User: ", query1)
	fmt.Println("Agent: ", must(ag.Run(&agent.RunContext{Thread: thread}, message.NewText(query1))))

	// Second conversation using the same thread - maintains context
	query2 := "How about London?"
	fmt.Println("User: ", query2)
	fmt.Println("Agent: ", must(ag.Run(&agent.RunContext{Thread: thread}, message.NewText(query2))))

	// Third conversation - agent should remember both previous cities
	query3 := "Which of the cities I asked about has better weather?"
	fmt.Println("User: ", query3)
	fmt.Println("Agent: ", must(ag.Run(&agent.RunContext{Thread: thread}, message.NewText(query3))))
	fmt.Println("Note: The agent remembers context from previous messages in the same thread.")
	fmt.Println()
}

// exampleWithExistingThreadMessages shows how to work with existing thread messages.
func exampleWithExistingThreadMessages() {
	fmt.Println("=== Existing Thread Messages Example ===")

	ag := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	// Start a conversation and build up message history
	thread := ag.NewThread()

	query1 := "What's the weather in Paris?"
	fmt.Println("User: ", query1)
	fmt.Println("Agent: ", must(ag.Run(&agent.RunContext{Thread: thread}, message.NewText(query1))))

	fmt.Println("\n--- Continuing with the same thread in a new agent instance ---")

	// Create a new agent instance but use the existing thread with its message history
	newAgent := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	// Use the same thread object which contains the conversation history
	query2 := "What was the last city I asked about?"
	fmt.Println("User: ", query2)
	fmt.Println("Agent: ", must(newAgent.Run(&agent.RunContext{Thread: thread}, message.NewText(query2))))
	fmt.Println("Note: The agent continues the conversation using the thread message history.")
	fmt.Println()
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
