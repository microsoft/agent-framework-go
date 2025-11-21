// Copyright (c) Microsoft. All rights reserved.

// Package mcp provides integration with the Model Context Protocol (MCP).
// It allows agents to connect to external MCP servers via stdio, HTTP, or WebSocket
// and expose their tools and prompts as agent.Tool instances.
package mcptool

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func Connect(ctx context.Context, transport mcp.Transport) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "agent-framework-go-mcp-client",
		Version: "1.0.0",
	}, nil)
	return client.Connect(ctx, transport, nil)
}

func ListTools(ctx context.Context, session *mcp.ClientSession) ([]tool.Tool, error) {
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Create agent.Tool instances for each MCP tool
	result := make([]tool.Tool, 0, len(toolsResult.Tools))
	for _, mcpTool := range toolsResult.Tools {
		agentTool := newMCPToolWrapper(session, mcpTool)
		result = append(result, agentTool)
	}

	return result, nil
}

// mcpContentToAgentContent converts MCP content types to agent framework content types.
func mcpContentToAgentContent(mcpContents []mcp.Content) []message.Content {
	if len(mcpContents) == 0 {
		return nil
	}

	result := make([]message.Content, 0, len(mcpContents))

	for _, c := range mcpContents {
		switch c := c.(type) {
		case *mcp.TextContent:
			result = append(result, &message.TextContent{
				Text: c.Text,
			})

		case *mcp.EmbeddedResource:
			// Handle embedded resources
			if c.Resource.Text != "" {
				result = append(result, &message.TextContent{
					Text: c.Resource.Text,
				})
			} else {
				result = append(result, &message.DataContent{
					Data:      c.Resource.Blob,
					MediaType: c.Resource.MIMEType,
					URI:       c.Resource.URI,
				})
			}

		default:
			// Unknown content type - convert to text
			result = append(result, &message.TextContent{
				Text: fmt.Sprintf("[Unknown MCP content type: %T]", c),
			})
		}
	}

	return result
}

var _ tool.Tool = (*mcpWrapper)(nil)
var _ tool.FuncTool = (*mcpWrapper)(nil)

// mcpWrapper wraps an MCP tool as an agent.Tool.
type mcpWrapper struct {
	session *mcp.ClientSession
	tool    *mcp.Tool
}

func newMCPToolWrapper(session *mcp.ClientSession, tool *mcp.Tool) *mcpWrapper {
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

func (w *mcpWrapper) ReturnSchema() any {
	return w.tool.OutputSchema
}

// Call implements the Func-like calling pattern for MCP tools.
func (w *mcpWrapper) Call(ctx context.Context, args map[string]any) (any, error) {
	// Call the MCP tool
	result, err := w.session.CallTool(ctx, &mcp.CallToolParams{
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
