// Copyright (c) Microsoft. All rights reserved.

// Package mcp provides integration with the Model Context Protocol (MCP).
// It allows agents to connect to external MCP servers via stdio, HTTP, or WebSocket
// and expose their tools and prompts as agent.Tool instances.
package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/microsoft/agent-framework/go/pkg/agent"
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

// Tool is the base interface for MCP tools that integrate with the agent framework.
type Tool interface {
	agent.Tool

	// Close terminates the connection to the MCP server.
	Close() error

	// LoadPrompts loads all prompts from the MCP server and returns them as agent.Tool instances.
	LoadPrompts(ctx context.Context, allowedPrompts []string, approval ApprovalMode, approvalCallback ApprovalCallback) ([]agent.Tool, error)

	// CallTool calls a specific tool on the MCP server.
	CallTool(ctx context.Context, toolName string, arguments map[string]any) (any, error)

	// GetPrompt retrieves a prompt from the MCP server.
	GetPrompt(ctx context.Context, promptName string, arguments map[string]any) (any, error)
}

// baseTool provides common functionality for all MCP tool implementations.
type baseTool struct {
	mu      sync.RWMutex
	client  *mcpsdk.Client
	session *mcpsdk.ClientSession

	samplingCallback SamplingCallback
	connected        bool
}

// Connect establishes the MCP connection.
func (t *baseTool) connect(ctx context.Context, transport mcpsdk.Transport, impl *mcpsdk.Implementation) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected {
		return fmt.Errorf("already connected")
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
		return fmt.Errorf("failed to connect to MCP server: %w", err)
	}

	t.session = session
	t.connected = true
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
	return nil
}

// LoadTools loads tools from the MCP server.
func (t *baseTool) LoadTools(ctx context.Context) ([]agent.Tool, error) {
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

// LoadPrompts loads prompts from the MCP server.
func (t *baseTool) LoadPrompts(ctx context.Context, allowedPrompts []string, approval ApprovalMode, approvalCallback ApprovalCallback) ([]agent.Tool, error) {
	t.mu.RLock()
	session := t.session
	t.mu.RUnlock()

	if session == nil {
		return nil, fmt.Errorf("not connected to MCP server")
	}

	// List available prompts
	promptsResult, err := session.ListPrompts(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}

	// Filter prompts if allowedPrompts is specified
	promptsToLoad := promptsResult.Prompts
	if len(allowedPrompts) > 0 {
		allowedSet := make(map[string]bool, len(allowedPrompts))
		for _, name := range allowedPrompts {
			allowedSet[name] = true
		}

		filtered := make([]*mcpsdk.Prompt, 0, len(promptsResult.Prompts))
		for _, prompt := range promptsResult.Prompts {
			if allowedSet[prompt.Name] {
				filtered = append(filtered, prompt)
			}
		}
		promptsToLoad = filtered
	}

	// Create agent.Tool instances for each MCP prompt
	result := make([]agent.Tool, 0, len(promptsToLoad))
	for _, mcpPrompt := range promptsToLoad {
		agentTool := newMCPPromptWrapper(t.session, mcpPrompt, approval, approvalCallback)
		result = append(result, agentTool)
	}

	return result, nil
}

// CallTool calls a specific tool on the MCP server.
func (t *baseTool) CallTool(ctx context.Context, toolName string, arguments map[string]any) (any, error) {
	t.mu.RLock()
	session := t.session
	t.mu.RUnlock()

	if session == nil {
		return nil, fmt.Errorf("not connected to MCP server")
	}

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call tool %q: %w", toolName, err)
	}

	return mcpContentToAgentContent(result.Content), nil
}

// GetPrompt retrieves a prompt from the MCP server.
func (t *baseTool) GetPrompt(ctx context.Context, promptName string, arguments map[string]any) (any, error) {
	t.mu.RLock()
	session := t.session
	t.mu.RUnlock()

	if session == nil {
		return nil, fmt.Errorf("not connected to MCP server")
	}

	args := make(map[string]string)
	for k, v := range arguments {
		if v, ok := v.(string); ok {
			args[k] = v
		}
	}

	result, err := session.GetPrompt(ctx, &mcpsdk.GetPromptParams{
		Name:      promptName,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt %q: %w", promptName, err)
	}

	return mcpPromptToAgentContent(result), nil
}
