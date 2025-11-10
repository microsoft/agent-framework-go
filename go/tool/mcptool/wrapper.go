// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ tool.Tool = (*mcpWrapper)(nil)
var _ tool.CallTool = (*mcpWrapper)(nil)

// mcpWrapper wraps an MCP tool as an agent.Tool.
type mcpWrapper struct {
	session *mcpsdk.ClientSession
	tool    *mcpsdk.Tool
}

func newMCPToolWrapper(session *mcpsdk.ClientSession, tool *mcpsdk.Tool) *mcpWrapper {
	return &mcpWrapper{
		session: session,
		tool:    tool,
	}
}

func (w *mcpWrapper) ToolInfo() (name string, description string) {
	return w.tool.Name, w.tool.Description
}

func (w *mcpWrapper) Schema() any {
	return w.tool.InputSchema
}

// Call implements the Func-like calling pattern for MCP tools.
func (w *mcpWrapper) Call(ctx context.Context, args map[string]any) (any, error) {
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
