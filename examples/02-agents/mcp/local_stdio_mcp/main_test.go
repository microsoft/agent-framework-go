// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMain lets this test binary double as a trivial stdio MCP server when the
// AF_STDIO_MCP_HELPER environment variable is set. This lets the client-side
// test spawn a real subprocess and talk to it over stdin/stdout via
// mcp.CommandTransport with no network access.
func TestMain(m *testing.M) {
	if os.Getenv("AF_STDIO_MCP_HELPER") == "1" {
		runStubMCPServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runStubMCPServer() {
	srv := mcp.NewServer(&mcp.Implementation{Name: "stub-stdio-server", Version: "1.0.0"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "Echoes a canned message.",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	_ = srv.Run(context.Background(), &mcp.StdioTransport{})
}

// TestLocalStdioMCPClient exercises the transport wiring used by this sample:
// mcp.CommandTransport -> mcptool.Connect -> mcptool.ListTools, and verifies
// that closing the session terminates the spawned child process.
func TestLocalStdioMCPClient(t *testing.T) {
	ctx := context.Background()

	// Re-invoke this test binary as the stdio MCP server (see TestMain).
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "AF_STDIO_MCP_HELPER=1")

	session, err := mcptool.Connect(ctx, &mcp.CommandTransport{Command: cmd})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("ListTools() returned %d tools, want 1", len(tools))
	}
	if got := tools[0].Name(); got != "echo" {
		t.Fatalf("tool name = %q, want %q", got, "echo")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("child process did not terminate after Close(); ProcessState = %v", cmd.ProcessState)
	}
}
