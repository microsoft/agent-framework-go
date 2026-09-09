// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to connect an agent to a local MCP server that runs as
// a subprocess, communicating over stdin/stdout via mcp.CommandTransport.
//
// It spawns the sibling "step10_as_mcp_tool" example (an agent exposed as an
// MCP tool over a stdio transport) as a child process, lists the tools it
// exposes, and attaches them to a Foundry agent. This mirrors the Python and
// .NET "stdio MCP client" samples, where a local command-based MCP server is
// launched and consumed by an agent.

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stdioServerDir resolves the sibling stdio MCP server example relative to this
// source file so the sample works regardless of the current working directory.
func stdioServerDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "agents", "step10_as_mcp_tool")
}

func main() {
	logger := demo.NewLogger(
		"Local stdio MCP Client",
		"Demonstrates connecting an agent to a local MCP server launched as a subprocess over stdio.",
		"Model", demo.FoundryModel,
	)

	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	// Launch the sibling stdio MCP server ("go run .") as a child process and
	// connect to it over stdin/stdout. Closing the session terminates the child.
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = stdioServerDir()
	cmd.Stderr = os.Stderr

	session, err := mcptool.Connect(ctx, &mcp.CommandTransport{Command: cmd})
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = session.Close() }()

	// Retrieve the list of tools exposed by the local MCP server.
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		demo.Panic(err)
	}

	// Create the agent with the tools discovered from the local MCP server.
	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant. Use the available tools to answer the user's request.",
			Config: agent.Config{
				Name:        "LocalMCPAgent",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
				Tools:       tools,
			},
		},
	)

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "Tell me a joke about a pirate.").Collect()
	demo.Response(resp, err)
}
