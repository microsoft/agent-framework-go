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
	"github.com/microsoft/agent-framework-go/message"
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

// Define a weather tool that requires user approval before execution
var weatherToolForApproval = functool.MustNew(&functool.Func{
	Name:        "weather",
	Description: "Get the current weather for a given location",
}, func(_ context.Context, location string) (string, error) {
	return fmt.Sprintf("The weather in %s is cloudy with a high of 15°C.", location), nil
})

// Demonstrates how to use function tools that require
// human-in-the-loop approval before execution.
//
// This is useful for:
//   - Sensitive operations that need user confirmation
//   - Actions with side effects (sending emails, making purchases, etc.)
//   - Compliance or audit requirements
//
// The flow is:
//  1. User sends a message
//  2. Agent decides to call a tool
//  3. Agent pauses and requests user approval (via FunctionApprovalRequestContent)
//  4. User approves or denies
//  5. If approved, tool executes and agent continues
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

	// Create Azure OpenAI agent with an APPROVAL-REQUIRED weather tool
	// Note: tool.ApprovalRequiredFunc wraps the tool to require user approval
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     apiKey,
		Endpoint:   endpoint,
		Model:      deployment,
		APIVersion: "2025-01-01-preview",
	}, chatagent.Options{
		Instructions: "You are a helpful weather assistant. Use the weather tool when asked about weather conditions.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{tool.ApprovalRequiredFunc(weatherToolForApproval)},
		},
	})

	printApprovalHeader(deployment)

	// Create a thread for the conversation
	thread := ag.NewThread()

	// ============================================================
	// Example 1: User APPROVES the tool call
	// ============================================================
	fmt.Printf("\n%s=== Example 1: User Approves Tool Call ===%s\n", colorCyan, colorReset)
	runWithApproval(ag, thread, "What's the weather like in Amsterdam?", true)

	// ============================================================
	// Example 2: User DENIES the tool call
	// ============================================================
	fmt.Printf("\n%s=== Example 2: User Denies Tool Call ===%s\n", colorCyan, colorReset)
	// Create a new thread for this example
	thread2 := ag.NewThread()
	runWithApproval(ag, thread2, "What's the weather like in Tokyo?", false)

	// ============================================================
	// Example 3: Query that doesn't need tool (no approval needed)
	// ============================================================
	fmt.Printf("\n%s=== Example 3: No Tool Needed (No Approval) ===%s\n", colorCyan, colorReset)
	thread3 := ag.NewThread()
	runWithApproval(ag, thread3, "What is the capital of France?", true) // approval won't be asked

	fmt.Printf("\n%s✅ Function tools with approval examples complete!%s\n", colorCyan, colorReset)
}

func printApprovalHeader(deployment string) {
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║   Azure OpenAI Agent - Function Tools with Approval        ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)
	fmt.Printf("%sModel: %s%s%s\n", colorGray, colorMagenta, deployment, colorReset)
	fmt.Printf("%sDemonstrating human-in-the-loop approval for tool calls%s\n", colorGray, colorReset)
	fmt.Printf("%s%s%s\n", colorGray, strings.Repeat("─", 60), colorReset)
}

func getTimestamp() string {
	return time.Now().Format("15:04:05")
}

// runWithApproval runs a query and handles any approval requests
func runWithApproval(ag agent.Agent, thread memory.Thread, query string, simulateApproval bool) {
	ctx := context.Background()

	// Print user message
	fmt.Printf("\n%s[%s]%s %s%sYou:%s %s\n",
		colorGray, getTimestamp(), colorReset,
		colorGreen, colorBold, colorReset, query)

	// Run the agent - this may return approval requests instead of a final answer
	resp, err := agent.RunText(ctx, ag, query, agentopt.Thread(thread))
	if err != nil {
		fmt.Printf("%s❌ Error: %v%s\n", colorRed, err, colorReset)
		return
	}

	// Check if there are any approval requests
	var userResponses []message.Content
	hasApprovalRequests := false

	for req := range resp.UserInputRequests() {
		hasApprovalRequests = true
		request, ok := req.(*message.FunctionApprovalRequestContent)
		if !ok {
			fmt.Printf("%s⚠️  Unexpected request type: %T%s\n", colorYellow, req, colorReset)
			continue
		}

		// Show the approval request
		fmt.Printf("%s[%s]%s %s%s🔔 Approval Request:%s\n",
			colorGray, getTimestamp(), colorReset,
			colorYellow, colorBold, colorReset)
		fmt.Printf("   The agent wants to call: %s%s%s\n", colorMagenta, request.FunctionCall.Name, colorReset)
		fmt.Printf("   With arguments: %s%s%s\n", colorGray, request.FunctionCall.Arguments, colorReset)

		// Simulate user approval/denial
		if simulateApproval {
			fmt.Printf("   %s✓ User approved the request%s\n", colorGreen, colorReset)
			userResponses = append(userResponses, request.Response(true))
		} else {
			fmt.Printf("   %s✗ User denied the request%s\n", colorRed, colorReset)
			userResponses = append(userResponses, request.Response(false))
		}
	}

	// If there were approval requests, continue the conversation with the responses
	if hasApprovalRequests && len(userResponses) > 0 {
		fmt.Printf("%s[%s]%s %s%sAssistant:%s ",
			colorGray, getTimestamp(), colorReset,
			colorBlue, colorBold, colorReset)

		resp, err = agent.Run(ctx, ag,
			agentopt.Message(message.New(userResponses...)),
			agentopt.Thread(thread))
		if err != nil {
			fmt.Printf("\n%s❌ Error: %v%s\n", colorRed, err, colorReset)
			return
		}
		fmt.Printf("%s\n", resp)
	} else if !hasApprovalRequests {
		// No approval needed - print the response directly
		fmt.Printf("%s[%s]%s %s%sAssistant:%s %s\n",
			colorGray, getTimestamp(), colorReset,
			colorBlue, colorBold, colorReset, resp)
	}
}
