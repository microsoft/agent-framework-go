package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

/*
OpenAI Chat CLI Example

This sample demonstrates an interactive CLI chat interface with an OpenAI agent.
Type your messages and get streaming responses. Type 'exit' or 'quit' to end the chat.
*/

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
	return fmt.Sprintf("The weather in %s is %s with a high of %d°C.",
		location, conditions[rand.IntN(len(conditions))], rand.IntN(21)+10), nil
})

func main() {
	// OpenAI configuration
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required. Get your key from https://platform.openai.com/account/api-keys")
	}

	ag := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Options{
		Instructions: "You are a helpful assistant with access to weather information. Be concise and friendly.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	// Create a thread to maintain conversation history
	thread := ag.NewThread()

	printWelcome()
	runChatLoop(ag, thread)
}

func printWelcome() {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║           OpenAI Chat CLI - Interactive Mode               ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("Type your message and press Enter to chat.")
	fmt.Println("Commands: 'exit' or 'quit' to end, 'clear' to reset conversation")
	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println()
}

func runChatLoop(ag agent.Agent, thread memory.Thread) {
	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)

	for {
		// Print user prompt
		fmt.Print("You: ")

		// Read user input
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())

		// Handle empty input
		if userInput == "" {
			continue
		}

		// Handle commands
		if userInput == "exit" || userInput == "quit" {
			fmt.Println("\n👋 Goodbye!")
			return
		}

		if userInput == "clear" {
			thread = ag.NewThread()
			fmt.Print("\n✨ Conversation cleared!\n\n")
			continue
		}

		// Get streaming response from agent
		fmt.Print("Assistant: ")

		hasError := false
		for update, err := range agent.RunTextStream(ctx, ag, userInput, agentopt.Thread(thread)) {
			if err != nil {
				fmt.Printf("\n❌ Error: %v\n", err)
				hasError = true
				break
			}
			// Print streaming text as it arrives
			fmt.Print(update)
		}

		if !hasError {
			fmt.Println() // New line after response
		}
		fmt.Println() // Extra spacing between exchanges
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("\n❌ Error reading input: %v\n", err)
	}
}
