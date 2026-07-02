// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var weatherTool = functool.MustNew(functool.Config{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

var _ = demo.NewLogger(
	"Chat CLI",
	"Runs an interactive Foundry-backed chat loop with a weather tool.",
	"Model", demo.FoundryModel,
)

func main() {
	token := demo.FoundryTokenCredential()

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant with access to weather information. Be concise and friendly.",
			Config: agent.Config{
				Tools: []tool.Tool{weatherTool},
			},
		},
	)

	runChatLoop(context.Background(), a)
}

func runChatLoop(ctx context.Context, a *agent.Agent) {
	// Create a session to maintain conversation history.
	session, err := a.CreateSession(ctx)
	if err != nil {
		demo.Panicf("failed to create chat session: %v", err)
	}

	demo.Assistant("Type 'clear' to reset the conversation or 'quit' to exit.")

	scanner := bufio.NewScanner(os.Stdin)

	for {
		// Print user prompt.
		fmt.Print("You: ")

		// Read user input.
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())

		// Handle empty input.
		if userInput == "" {
			continue
		}

		// Handle commands.
		if userInput == "exit" || userInput == "quit" {
			fmt.Println("\n👋 Goodbye!")
			return
		}

		if userInput == "clear" {
			session, err = a.CreateSession(ctx)
			if err != nil {
				demo.Panicf("failed to create chat session: %v", err)
			}
			fmt.Print("\n✨ Conversation cleared!\n\n")
			continue
		}

		// Get streaming response from agent.
		fmt.Print("Assistant: ")

		hasError := false
		for update, err := range a.RunText(ctx, userInput, agent.WithSession(session), agent.Stream(true)) {
			if err != nil {
				fmt.Printf("\n❌ Error: %v\n", err)
				hasError = true
				break
			}
			// Print streaming text as it arrives.
			fmt.Print(update)
		}

		if !hasError {
			fmt.Println() // New line after response
		}
		fmt.Println() // Extra spacing between exchanges
	}

	if err := scanner.Err(); err != nil {
		demo.Panicf("failed to read input: %v", err)
	}
}
