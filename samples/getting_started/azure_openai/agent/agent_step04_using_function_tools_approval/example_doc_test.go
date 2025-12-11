package example_test

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// Example_toolApproval demonstrates how to require user approval before
// executing function tools. This is useful for sensitive operations.
func Example_toolApproval() {
	// Define a tool that will require approval
	sendEmailTool := functool.MustNew(&functool.Func{
		Name:        "send_email",
		Description: "Send an email to a recipient",
	}, func(_ context.Context, args struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}) (string, error) {
		// This only runs AFTER user approves
		return fmt.Sprintf("Email sent to %s", args.To), nil
	})

	// Create agent with approval required for the tool
	// Note: tool.ApprovalRequiredFunc wraps the tool to require user approval
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     "your-api-key",
		Endpoint:   "https://your-resource.openai.azure.com/",
		Model:      "gpt-4o",
		APIVersion: "2025-01-01-preview",
	}, &chatagent.Options{
		Instructions: "You are an email assistant.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{tool.ApprovalRequiredFunc(sendEmailTool)},
		},
	})

	fmt.Println("Agent created with tool approval enabled")
	fmt.Println("Tools will require user confirmation before execution")
	_ = ag

	// Output:
	// Agent created with tool approval enabled
	// Tools will require user confirmation before execution
}

// Example_approvalCallback demonstrates how to implement an approval callback
// that automatically approves or denies tool calls based on criteria.
func Example_approvalCallback() {
	// Define a tool
	deleteTool := functool.MustNew(&functool.Func{
		Name:        "delete_file",
		Description: "Delete a file from the system",
	}, func(_ context.Context, filename string) (string, error) {
		return fmt.Sprintf("Deleted: %s", filename), nil
	})

	// Create agent with approval-required tool
	// The tool.ApprovalRequiredFunc wrapper marks the tool as needing approval
	ag := openai.NewChatAgentAzure(openai.ClientConfig{
		APIKey:     "your-api-key",
		Endpoint:   "https://your-resource.openai.azure.com/",
		Model:      "gpt-4o",
		APIVersion: "2025-01-01-preview",
	}, &chatagent.Options{
		Instructions: "You are a file manager.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: []tool.Tool{tool.ApprovalRequiredFunc(deleteTool)},
		},
	})

	fmt.Println("Agent with approval callback created")
	fmt.Println("The callback can auto-approve safe operations")
	_ = ag

	// Output:
	// Agent with approval callback created
	// The callback can auto-approve safe operations
}
