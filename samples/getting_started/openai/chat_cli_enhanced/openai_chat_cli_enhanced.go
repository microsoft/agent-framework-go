package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

/*
OpenAI Chat CLI Enhanced Example

An enhanced interactive CLI chat interface.
Type your messages and get streaming responses.
Type 'exit' or 'quit' to end the chat.

*/

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
	return fmt.Sprintf("The weather in %s is %s with a high of %d°C.",
		location, conditions[rand.Intn(len(conditions))], rand.Intn(21)+10), nil
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
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║           OpenAI Chat CLI - Interactive Mode               ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)
	fmt.Printf("%sType your message and press Enter to chat.%s\n", colorGray, colorReset)
	fmt.Printf("%sCommands: %sexit%s/%squit%s to end, %sclear%s to reset conversation%s\n",
		colorGray, colorYellow, colorGray, colorYellow, colorGray, colorYellow, colorGray, colorReset)
	fmt.Println()
	fmt.Printf("%s%s%s\n", colorGray, strings.Repeat("─", 60), colorReset)
	fmt.Println()
}

func getTimestamp() string {
	return time.Now().Format("15:04:05")
}

func runChatLoop(ag agent.Agent, thread memory.Thread) {
	ctx := context.Background()
	scanner := bufio.NewScanner(os.Stdin)
	messageCount := 0

	for {
		// Print user prompt with timestamp
		fmt.Printf("%s[%s]%s %s%sYou:%s ",
			colorGray, getTimestamp(), colorReset,
			colorGreen, colorBold, colorReset)

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
			fmt.Printf("\n%s👋 Goodbye! Had %d message(s) in this conversation.%s\n",
				colorCyan, messageCount, colorReset)
			return
		}

		if userInput == "clear" {
			thread = ag.NewThread()
			messageCount = 0
			fmt.Printf("\n%s✨ Conversation cleared!%s\n\n", colorYellow, colorReset)
			continue
		}

		if userInput == "help" {
			printHelp()
			continue
		}

		messageCount++

		// Get streaming response from agent
		fmt.Printf("%s[%s]%s %s%sAssistant:%s ",
			colorGray, getTimestamp(), colorReset,
			colorBlue, colorBold, colorReset)

		hasError := false
		for update, err := range agent.RunTextStream(ctx, ag, userInput, agentopt.Thread(thread)) {
			if err != nil {
				fmt.Printf("\n%s❌ Error: %v%s\n", colorRed, err, colorReset)
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
		fmt.Printf("\n%s❌ Error reading input: %v%s\n", colorRed, err, colorReset)
	}
}

func printHelp() {
	fmt.Printf("\n%s%sAvailable Commands:%s\n", colorCyan, colorBold, colorReset)
	fmt.Printf("  %sexit%s / %squit%s  - End the chat session\n", colorYellow, colorReset, colorYellow, colorReset)
	fmt.Printf("  %sclear%s        - Clear conversation history\n", colorYellow, colorReset)
	fmt.Printf("  %shelp%s         - Show this help message\n\n", colorYellow, colorReset)
}
