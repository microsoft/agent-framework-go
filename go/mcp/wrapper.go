// Copyright (c) Microsoft. All rights reserved.

package mcp

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/agentext"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ agent.Tool = (*mcpToolWrapper)(nil)
var _ agentext.CallTool = (*mcpToolWrapper)(nil)

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

func (w *mcpToolWrapper) Schema() any {
	return w.tool.InputSchema
}

// Call implements the Func-like calling pattern for MCP tools.
func (w *mcpToolWrapper) Call(ctx context.Context, args map[string]any) (any, error) {
	// Call the MCP tool
	result, err := w.session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      w.tool.Name,
		Arguments: args,
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
