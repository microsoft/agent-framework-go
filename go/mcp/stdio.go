// Copyright (c) Microsoft. All rights reserved.

package mcp

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/microsoft/agent-framework/go/agent"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ agent.LoaderTool = (*StdioTool)(nil)
var _ agent.InitTool = (*StdioTool)(nil)

// StdioTool connects to an MCP server via stdio (process communication).
// This is the most common transport for local MCP servers.
type StdioTool struct {
	tool *baseTool

	// Command is the path to the MCP server executable.
	Command string

	// Args are the command-line arguments to pass to the server.
	Args []string

	// Env are additional environment variables for the server process.
	Env []string

	// Name is the client implementation name.
	Name string

	// Version is the client implementation version.
	Version string
}

// ToolInfo implements the agent.Tool interface.
// This returns a generic name for the MCP connection itself, not individual tools.
func (t StdioTool) ToolInfo() (name string, description string) {
	return "mcp_stdio", fmt.Sprintf("MCP connection via stdio to %s", t.Command)
}

func (t StdioTool) Init(ctx context.Context) error {
	return t.connect(ctx)
}

func (t StdioTool) LoadTools(ctx context.Context) ([]agent.Tool, error) {
	return t.tool.loadTools(ctx)
}

func (t StdioTool) Schema() map[string]any {
	return map[string]any{
		"command": t.Command,
		"args":    t.Args,
		"env":     t.Env,
	}
}

// connect establishes a connection to the MCP server via stdio.
func (t StdioTool) connect(ctx context.Context) error {
	// Create the command
	cmd := exec.CommandContext(ctx, t.Command, t.Args...)

	// Add environment variables
	cmd.Env = append(cmd.Environ(), t.Env...)

	// Create the transport
	transport := &mcpsdk.CommandTransport{
		Command: cmd,
	}

	// Connect using the base tool
	return t.tool.connect(ctx, transport, &mcpsdk.Implementation{
		Name:    t.Name,
		Version: t.Version,
	})
}

// NewStdioTool creates a new MCP tool that connects via stdio.
func NewStdioTool(command string, args []string, env []string, samplingCallback SamplingCallback) StdioTool {
	return StdioTool{
		Command: command,
		Args:    args,
		Env:     env,
		tool:    new(baseTool),
	}
}
