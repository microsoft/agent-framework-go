// Copyright (c) Microsoft. All rights reserved.

package mcp

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ agent.Tool = (*mcpToolWrapper)(nil)
var _ agent.CallTool = (*mcpToolWrapper)(nil)

// mcpToolWrapper wraps an MCP tool as an agent.Tool.
type mcpToolWrapper struct {
	session *mcpsdk.ClientSession
	tool    *mcpsdk.Tool
}

func newMCPToolWrapper(session *mcpsdk.ClientSession, tool *mcpsdk.Tool) *mcpToolWrapper {
	return &mcpToolWrapper{
		session: session,
		tool:    tool,
	}
}

func (w *mcpToolWrapper) ToolInfo() (name string, description string) {
	return w.tool.Name, w.tool.Description
}

func (w *mcpToolWrapper) Schema() map[string]any {
	return w.tool.InputSchema.(map[string]any)
}

// Call implements the Func-like calling pattern for MCP tools.
func (w *mcpToolWrapper) Call(ctx context.Context, arguments map[string]any) (any, error) {
	// Call the MCP tool
	result, err := w.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      w.tool.Name,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("MCP tool call failed: %w", err)
	}

	if result.IsError {
		return nil, fmt.Errorf("MCP tool returned error")
	}

	// Convert MCP content to agent content
	return mcpContentToAgentContent(result.Content), nil
}

var _ agent.Tool = (*mcpPromptWrapper)(nil)

// mcpPromptWrapper wraps an MCP prompt as an agent.Tool.
type mcpPromptWrapper struct {
	session          *mcpsdk.ClientSession
	prompt           *mcpsdk.Prompt
	approval         ApprovalMode
	approvalCallback ApprovalCallback
}

func newMCPPromptWrapper(session *mcpsdk.ClientSession, prompt *mcpsdk.Prompt, approval ApprovalMode, approvalCallback ApprovalCallback) *mcpPromptWrapper {
	return &mcpPromptWrapper{
		session:          session,
		prompt:           prompt,
		approval:         approval,
		approvalCallback: approvalCallback,
	}
}

func (w *mcpPromptWrapper) ToolInfo() (name string, description string) {
	return w.prompt.Name, w.prompt.Description
}

func (w *mcpPromptWrapper) Schema() map[string]any {
	if len(w.prompt.Arguments) == 0 {
		return nil
	}
	props := make(map[string]any)
	props["type"] = "object"

	properties := make(map[string]any)
	required := make([]string, 0)

	for _, arg := range w.prompt.Arguments {
		argProps := make(map[string]any)
		argProps["type"] = "string" // MCP prompt arguments are typically strings

		if arg.Description != "" {
			argProps["description"] = arg.Description
		}

		properties[arg.Name] = argProps

		if arg.Required {
			required = append(required, arg.Name)
		}
	}

	props["properties"] = properties
	if len(required) > 0 {
		props["required"] = required
	}

	return props
}

// Call implements the Func-like calling pattern for MCP prompts.
func (w *mcpPromptWrapper) Call(ctx context.Context, arguments map[string]any) (any, error) {
	// Check if approval is required
	if w.approval == ApprovalModeAlwaysRequire && w.approvalCallback != nil {
		approved, err := w.approvalCallback(ctx, w.prompt.Name, arguments)
		if err != nil {
			return nil, fmt.Errorf("approval callback failed: %w", err)
		}
		if !approved {
			return nil, fmt.Errorf("prompt call to %q was not approved", w.prompt.Name)
		}
	}

	args := make(map[string]string)
	for k, v := range arguments {
		if v, ok := v.(string); ok {
			args[k] = v
		}
	}

	// Get the MCP prompt
	result, err := w.session.GetPrompt(ctx, &mcpsdk.GetPromptParams{
		Name:      w.prompt.Name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("MCP prompt call failed: %w", err)
	}

	// Convert MCP prompt result to agent content
	return mcpPromptToAgentContent(result), nil
}
