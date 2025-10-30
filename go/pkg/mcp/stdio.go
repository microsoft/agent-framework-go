// Copyright (c) Microsoft. All rights reserved.

package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/microsoft/agent-framework/go/pkg/internal/exp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ Tool = (*StdioTool)(nil)
var _ exp.LoaderTool = (*StdioTool)(nil)
var _ exp.InitTool = (*StdioTool)(nil)

// StdioTool connects to an MCP server via stdio (process communication).
// This is the most common transport for local MCP servers.
type StdioTool struct {
	baseTool

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
func (t *StdioTool) ToolInfo() (name string, description string) {
	return "mcp_stdio", fmt.Sprintf("MCP connection via stdio to %s", t.Command)
}

// Properties implements the agent.Tool interface.
func (t *StdioTool) Properties() map[string]any {
	return map[string]any{
		"command": t.Command,
		"args":    t.Args,
	}
}

func (t *StdioTool) Init(ctx context.Context) error {
	return t.connect(ctx)
}

func (t *StdioTool) Schema() map[string]any {
	return map[string]any{
		"command": t.Command,
		"args":    t.Args,
		"env":     t.Env,
	}
}

// connect establishes a connection to the MCP server via stdio.
func (t *StdioTool) connect(ctx context.Context) error {
	// Create the command
	cmd := exec.CommandContext(ctx, t.Command, t.Args...)

	// Add environment variables
	if len(t.Env) > 0 {
		cmd.Env = append(cmd.Environ(), t.Env...)
	}

	// Create the transport
	transport := &mcpsdk.CommandTransport{
		Command: cmd,
	}

	// Create implementation info
	impl := &mcpsdk.Implementation{
		Name:    t.Name,
		Version: t.Version,
	}
	if impl.Name == "" {
		impl.Name = "agent-framework-mcp-client"
	}
	if impl.Version == "" {
		impl.Version = "1.0.0"
	}

	// Connect using the base tool
	return t.baseTool.connect(ctx, transport, impl)
}

// NewStdioTool creates a new MCP tool that connects via stdio.
func NewStdioTool(command string, args []string, env []string, samplingCallback SamplingCallback) *StdioTool {
	t := &StdioTool{
		Command: command,
		Args:    args,
		Env:     env,
		baseTool: baseTool{
			samplingCallback: samplingCallback,
		},
	}
	runtime.AddCleanup(t, func(client *mcpsdk.ClientSession) { client.Close() }, t.baseTool.session)
	return t
}
