package example_test

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"testing"
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

// Define a weather tool that the agent can call
var weatherTool = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	fmt.Printf("%s🌤️  [Tool Called: weather] Location: %s%s\n", colorYellow, location, colorReset)
	conditions := []string{"sunny", "cloudy", "rainy", "stormy"}
	return fmt.Sprintf("The weather in %s is %s with a high of %d°C.", location, conditions[rand.Intn(len(conditions))], rand.Intn(21)+10), nil
})

// TestFunctionTools demonstrates how to create an agent with function tools
// that the AI can call to perform actions or retrieve information.
//
// This sample shows:
//   - Defining a function tool with functool.MustNew
//   - Attaching tools to an agent via ChatOptions
//   - The agent automatically calling tools when appropriate
//   - Both streaming and non-streaming responses with tool calling
//
// Required environment variables:
//   - AZURE_OPENAI_API_KEY
//   - AZURE_OPENAI_ENDPOINT
//   - AZURE_OPENAI_DEPLOYMENT_NAME
func TestFunctionTools(t *testing.T) {
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

	printHeader(deployment)

	// Example 1: Tool WILL be called (weather-related query)
	runNonStreaming(ag, "What's the weather like in Seattle?")

	// Example 2: Tool WILL be called (streaming)
	runStreaming(ag, "What's the weather like in Portland?")

	// Example 3: Tool will NOT be called (general conversation)
	runNonStreaming(ag, "Hello! How are you today?")

	// Example 4: Tool will NOT be called (unrelated question)
	runNonStreaming(ag, "What is the capital of France?")

	// Example 5: Tool WILL be called (implicit weather request)
	runNonStreaming(ag, "Should I bring an umbrella in Boston today?")

	fmt.Printf("\n%s✅ Function tools examples complete!%s\n", colorCyan, colorReset)
}

func printHeader(deployment string) {
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║     Azure OpenAI Agent - Function Tools Example            ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)
	fmt.Printf("%sModel: %s%s%s\n", colorGray, colorMagenta, deployment, colorReset)
	fmt.Printf("%sDemonstrating function tool calling with weather tool%s\n", colorGray, colorReset)
	fmt.Printf("%s%s%s\n", colorGray, strings.Repeat("─", 60), colorReset)
}

func timestamp() string {
	return time.Now().Format("15:04:05")
}

func runNonStreaming(ag agent.Agent, query string) {
	ctx := context.Background()
	fmt.Printf("\n%s[%s]%s %s%sYou:%s %s\n",
		colorGray, timestamp(), colorReset,
		colorGreen, colorBold, colorReset, query)

	fmt.Printf("%s[%s]%s %s%sAssistant:%s ",
		colorGray, timestamp(), colorReset,
		colorBlue, colorBold, colorReset)

	resp, err := agent.RunText(ctx, ag, query)
	if err != nil {
		fmt.Printf("\n%s❌ Error: %v%s\n", colorRed, err, colorReset)
		return
	}
	fmt.Printf("%s\n", resp)
}

func runStreaming(ag agent.Agent, query string) {
	ctx := context.Background()
	fmt.Printf("\n%s[%s]%s %s%sYou:%s %s\n",
		colorGray, timestamp(), colorReset,
		colorGreen, colorBold, colorReset, query)

	fmt.Printf("%s[%s]%s %s%sAssistant:%s ",
		colorGray, timestamp(), colorReset,
		colorBlue, colorBold, colorReset)

	for update, err := range agent.RunTextStream(ctx, ag, query) {
		if err != nil {
			fmt.Printf("\n%s❌ Error: %v%s\n", colorRed, err, colorReset)
			return
		}
		fmt.Print(update)
	}
	fmt.Println()
}
