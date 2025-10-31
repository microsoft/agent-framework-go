// Copyright (c) Microsoft. All rights reserved.

// Package mcp provides integration with the Model Context Protocol (MCP).
// It allows agents to connect to external MCP servers via stdio, HTTP, or WebSocket
// and expose their tools and prompts as agent.Tool instances.
package mcp

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/microsoft/agent-framework/go/agent"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ApprovalMode determines when user approval is required for tool calls.
type ApprovalMode string

const (
	// ApprovalModeAlwaysRequire always requires approval before executing tools.
	ApprovalModeAlwaysRequire ApprovalMode = "always_require"
	// ApprovalModeNeverRequire never requires approval (auto-execute).
	ApprovalModeNeverRequire ApprovalMode = "never_require"
)

// ApprovalCallback is called when a tool requires approval before execution.
// It should return true to approve the execution, false to deny it.
type ApprovalCallback func(ctx context.Context, toolName string, arguments map[string]any) (bool, error)

// SamplingCallback is called when the MCP server requests AI completion.
type SamplingCallback func(ctx context.Context, params *mcpsdk.CreateMessageParams) (*mcpsdk.CreateMessageResult, error)

// baseTool provides common functionality for all MCP tool implementations.
type baseTool struct {
	mu      sync.RWMutex
	client  *mcpsdk.Client
	session *mcpsdk.ClientSession

	samplingCallback SamplingCallback
	connected        bool
	connectedError   error
}

// Connect establishes the MCP connection.
func (t *baseTool) connect(ctx context.Context, transport mcpsdk.Transport, impl *mcpsdk.Implementation) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected {
		return t.connectedError
	}

	if impl.Name == "" {
		impl.Name = "agent-framework-mcp-client"
	}
	if impl.Version == "" {
		impl.Version = "1.0.0"
	}

	// Create client with sampling callback if provided
	var opts *mcpsdk.ClientOptions
	if t.samplingCallback != nil {
		opts = &mcpsdk.ClientOptions{
			CreateMessageHandler: func(ctx context.Context, req *mcpsdk.CreateMessageRequest) (*mcpsdk.CreateMessageResult, error) {
				return t.samplingCallback(ctx, req.Params)
			},
		}
	}

	t.client = mcpsdk.NewClient(impl, opts)

	session, err := t.client.Connect(ctx, transport, nil)
	if err != nil {
		t.connectedError = fmt.Errorf("failed to connect to MCP server: %w", err)
		return t.connectedError
	}

	t.session = session
	t.connected = true

	runtime.AddCleanup(t, func(client *mcpsdk.ClientSession) { client.Close() }, t.session)
	return nil
}

// Close terminates the MCP connection.
func (t *baseTool) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return nil
	}

	if t.session != nil {
		if err := t.session.Close(); err != nil {
			return fmt.Errorf("failed to close MCP session: %w", err)
		}
		t.session = nil
	}

	t.connected = false
	t.connectedError = nil
	return nil
}

// loadTools loads tools from the MCP server.
func (t *baseTool) loadTools(ctx context.Context) ([]agent.Tool, error) {
	t.mu.RLock()
	session := t.session
	t.mu.RUnlock()

	if session == nil {
		return nil, fmt.Errorf("not connected to MCP server")
	}

	// List available tools
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Create agent.Tool instances for each MCP tool
	result := make([]agent.Tool, 0, len(toolsResult.Tools))
	for _, mcpTool := range toolsResult.Tools {
		agentTool := newMCPToolWrapper(t.session, mcpTool)
		result = append(result, agentTool)
	}

	return result, nil
}
