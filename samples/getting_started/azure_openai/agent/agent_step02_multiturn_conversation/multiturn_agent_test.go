package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/openai"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorGreen   = "\033[32m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorGray    = "\033[90m"
	colorBold    = "\033[1m"
)

// Demonstrates a multi-turn conversation where
// the agent maintains context across multiple messages using a thread.
//
// This mirrors the .NET sample that shows:
//   - Creating a thread to preserve conversation context
//   - Making multiple calls that reference previous messages
//   - Both streaming and non-streaming multi-turn conversations
//
// Required environment variables:
//   - AZURE_OPENAI_API_KEY
//   - AZURE_OPENAI_ENDPOINT
//   - AZURE_OPENAI_DEPLOYMENT_NAME
func main() {
	// Azure OpenAI configuration
	apiKey := os.Getenv("AZURE_OPENAI_API_KEY")
	endpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
	deployment := os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")

	if apiKey == "" || endpoint == "" || deployment == "" {
		log.Fatal("Required environment variables:\n" +
			"  AZURE_OPENAI_API_KEY\n" +
			"  AZURE_OPENAI_ENDPOINT (e.g., https://your-resource.openai.azure.com/)\n" +
			"  AZURE_OPENAI_DEPLOYMENT_NAME (e.g., gpt-4o)")
	}

	// Create Azure OpenAI agent (like a joke-telling agent)
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     apiKey,
		Endpoint:   endpoint,
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
	}, chatagent.Options{
		Instructions: "You are good at telling jokes.",
		Name:         "Joker",
	})

	printHeader(deployment)

	// ============================================================
	// Example 1: Non-streaming multi-turn conversation
	// ============================================================
	fmt.Printf("\n%s=== Non-Streaming Multi-Turn Conversation ===%s\n", colorCyan, colorReset)

	// Create a thread to preserve conversation context
	thread := ag.NewThread()

	// First message
	runWithThread(ag, thread, "Tell me a joke about a pirate.")

	// Second message - references the previous joke (agent remembers context)
	runWithThread(ag, thread, "Now add some emojis to the joke and tell it in the voice of a pirate's parrot.")

	// ============================================================
	// Example 2: Streaming multi-turn conversation
	// ============================================================
	fmt.Printf("\n%s=== Streaming Multi-Turn Conversation ===%s\n", colorCyan, colorReset)

	// Create a NEW thread for a fresh conversation
	streamThread := ag.NewThread()

	// First streaming message
	runStreamWithThread(ag, streamThread, "Tell me a joke about a programmer.")

	// Second streaming message - references the previous joke
	runStreamWithThread(ag, streamThread, "Now make it about a Go programmer specifically.")

	// ============================================================
	// Example 3: Demonstrate context is preserved
	// ============================================================
	fmt.Printf("\n%s=== Context Preservation Demo ===%s\n", colorCyan, colorReset)

	contextThread := ag.NewThread()

	runWithThread(ag, contextThread, "My name is Alice and I love cats.")
	runWithThread(ag, contextThread, "What's my name and what do I love?")

	fmt.Printf("\n%s✅ Multi-turn conversation examples complete!%s\n", colorCyan, colorReset)
}

func printHeader(deployment string) {
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║     Azure OpenAI Agent - Multi-Turn Conversation           ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)
	fmt.Printf("%sModel: %s%s%s\n", colorGray, colorMagenta, deployment, colorReset)
	fmt.Printf("%sDemonstrating conversation context preservation with threads%s\n", colorGray, colorReset)
	fmt.Printf("%s%s%s\n", colorGray, strings.Repeat("─", 60), colorReset)
}

func timestamp() string {
	return time.Now().Format("15:04:05")
}

// runWithThread runs a non-streaming query using the provided thread for context
func runWithThread(ag agent.Agent, thread memory.Thread, query string) {
	ctx := context.Background()

	fmt.Printf("\n%s[%s]%s %s%sYou:%s %s\n",
		colorGray, timestamp(), colorReset,
		colorGreen, colorBold, colorReset, query)

	fmt.Printf("%s[%s]%s %s%sAssistant:%s ",
		colorGray, timestamp(), colorReset,
		colorBlue, colorBold, colorReset)

	resp, err := agent.RunText(ctx, ag, query, agentopt.Thread(thread))
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("%s\n", resp)
}

// runStreamWithThread runs a streaming query using the provided thread for context
func runStreamWithThread(ag agent.Agent, thread memory.Thread, query string) {
	ctx := context.Background()

	fmt.Printf("\n%s[%s]%s %s%sYou:%s %s\n",
		colorGray, timestamp(), colorReset,
		colorGreen, colorBold, colorReset, query)

	fmt.Printf("%s[%s]%s %s%sAssistant:%s ",
		colorGray, timestamp(), colorReset,
		colorBlue, colorBold, colorReset)

	for update, err := range agent.RunTextStream(ctx, ag, query, agentopt.Thread(thread)) {
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Print(update)
	}
	fmt.Println()
}
