package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorGray    = "\033[90m"
	colorBold    = "\033[1m"
)

var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

// TestAzureChatBasic demonstrates basic usage of Azure OpenAI Agent for
// direct chat-based interactions, showing both streaming and non-streaming
// responses with tool calling.
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

	// Create Azure OpenAI agent with weather tool
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     apiKey,
		Endpoint:   endpoint,
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
	}, chatagent.Options{
		Instructions: "You are a helpful weather assistant. Use the weather tool when asked about weather conditions.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{weatherTool},
		},
	})

	printWelcome(deployment)

	// Example 1: Tool WILL be called (weather-related query)
	nonStreamingExample(ag, "What's the weather like in Seattle?")

	// Example 2: Tool WILL be called (streaming)
	streamingExample(ag, "What's the weather like in Portland?")

	// Example 3: Tool will NOT be called (general conversation)
	nonStreamingExample(ag, "Hello! How are you today?")

	// Example 4: Tool will NOT be called (unrelated question)
	nonStreamingExample(ag, "What is the capital of France?")

	// Example 5: Tool WILL be called (implicit weather request)
	nonStreamingExample(ag, "Should I bring an umbrella in Boston today?")

	fmt.Printf("\n%s✅ Examples complete!%s\n", colorCyan, colorReset)
}

func printWelcome(deployment string) {
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║       Azure OpenAI Chat Agent - Basic Examples             ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)
	fmt.Printf("%sModel: %s%s%s\n", colorGray, colorMagenta, deployment, colorReset)
	fmt.Printf("%sDemonstrating streaming & non-streaming with tool calling%s\n", colorGray, colorReset)
	fmt.Println()
	fmt.Printf("%s%s%s\n", colorGray, strings.Repeat("─", 60), colorReset)
}

func getTimestamp() string {
	return time.Now().Format("15:04:05")
}

func nonStreamingExample(ag agent.Agent, query string) {
	ctx := context.Background()
	fmt.Printf("\n%s[%s]%s %s%sYou:%s %s\n",
		colorGray, getTimestamp(), colorReset,
		colorGreen, colorBold, colorReset, query)

	fmt.Printf("%s[%s]%s %s%sAssistant:%s ",
		colorGray, getTimestamp(), colorReset,
		colorBlue, colorBold, colorReset)

	resp, err := agent.RunText(ctx, ag, query)
	if err != nil {
		fmt.Printf("\n%s❌ Error: %v%s\n", colorRed, err, colorReset)
		return
	}
	fmt.Printf("%s\n", resp)
}

func streamingExample(ag agent.Agent, query string) {
	ctx := context.Background()
	fmt.Printf("\n%s[%s]%s %s%sYou:%s %s\n",
		colorGray, getTimestamp(), colorReset,
		colorGreen, colorBold, colorReset, query)

	fmt.Printf("%s[%s]%s %s%sAssistant:%s ",
		colorGray, getTimestamp(), colorReset,
		colorBlue, colorBold, colorReset)

	for update, err := range agent.RunTextStream(ctx, ag, query) {
		if err != nil {
			fmt.Printf("\n%s❌ Error: %v%s\n", colorRed, err, colorReset)
			return
		}
		fmt.Print(update)
	}
	fmt.Print("\n")
}
