package example_test

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// Example_functionTool demonstrates how to create an agent with a function tool
// that the AI can call to perform actions or retrieve information.
func Example_functionTool() {
	// Define a simple tool that the agent can call
	greetTool := functool.MustNew(&functool.Func{
		Name:        "greet",
		Description: "Greet a person by name",
	}, func(_ context.Context, name string) (string, error) {
		return fmt.Sprintf("Hello, %s! Welcome!", name), nil
	})

	// Create an agent with the tool
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     "your-api-key",
		Endpoint:   "https://your-resource.openai.azure.com/",
		Model:      "gpt-4o",
		APIVersion: "2025-01-01-preview",
	}, &chatagent.Options{
		Instructions: "You are a friendly greeter. Use the greet tool to welcome people.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{greetTool},
		},
	})

	ctx := context.Background()
	response, _ := agent.RunText(ctx, ag, "Please greet Alice")
	fmt.Println("Response:", response)

	// Output:
	// Response: Hello, Alice! Welcome!
}

// Example_multipleTools demonstrates how to provide multiple tools to an agent.
func Example_multipleTools() {
	// Define multiple tools
	addTool := functool.MustNew(&functool.Func{
		Name:        "add",
		Description: "Add two numbers together",
	}, func(_ context.Context, args struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (int, error) {
		return args.A + args.B, nil
	})

	multiplyTool := functool.MustNew(&functool.Func{
		Name:        "multiply",
		Description: "Multiply two numbers",
	}, func(_ context.Context, args struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (int, error) {
		return args.A * args.B, nil
	})

	// Create agent with multiple tools
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     "your-api-key",
		Endpoint:   "https://your-resource.openai.azure.com/",
		Model:      "gpt-4o",
		APIVersion: "2025-01-01-preview",
	}, &chatagent.Options{
		Instructions: "You are a calculator. Use the available tools to perform math.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{addTool, multiplyTool},
		},
	})

	ctx := context.Background()
	response, _ := agent.RunText(ctx, ag, "What is 5 + 3?")
	fmt.Println("Result:", response)

	// Output:
	// Result: 5 + 3 = 8
}
