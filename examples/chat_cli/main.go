package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var logger = demo.NewLogger(
	"Chat CLI - Interactive Mode",
	"Type your message and press Enter to chat.\nCommands: 'exit' or 'quit' to end, 'clear' to reset conversation",
	"Model", "gpt-4o-mini",
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

func main() {
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Config{
		Instructions: "You are a helpful assistant with access to weather information. Be concise and friendly.",
		RunOptions: []agentopt.RunOption{
			agentopt.Tool(weatherTool),
		},
	})

	runChatLoop(context.Background(), a)
}

func runChatLoop(ctx context.Context, a agent.Agent) {
	// Create a thread to maintain conversation history
	thread, err := a.NewThread(ctx)
	if err != nil {
		panic(err)
	}

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
			thread, err = a.NewThread(ctx)
			if err != nil {
				panic(err)
			}
			fmt.Print("\n✨ Conversation cleared!\n\n")
			continue
		}

		// Get streaming response from agent
		fmt.Print("Assistant: ")

		hasError := false
		for update, err := range agent.RunTextStream(ctx, a, userInput, agentopt.Thread(thread)) {
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
