// Copyright (c) Microsoft. All rights reserved.

// This sample demonstrates MCP long-running task support.
//
// A small MCP server (hosted in this same executable when launched with
// "--server", see server.go) exposes a single task-capable tool,
// "AnalyzeDataset", that simulates ~15 seconds of work. The client (default
// mode) connects to it over stdio, wraps its tools with
// mcptool.ListToolsWithTasks, hands them to an agent, and runs the agent both
// collecting the full response and streaming updates.
//
// In both cases the task-aware tools transparently:
//  1. Call tools/call advertising the io.modelcontextprotocol/tasks extension.
//  2. Accept either an inline result or a task handle.
//  3. Poll tasks/get until the final result is available.
//
// No application-level polling loop or continuation tokens are required.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const prompt = "Analyze the dataset named 'sales-2025-q1' and summarize the findings."

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--server" {
		runServer()
		return
	}

	logger := demo.NewLogger(
		"MCP Long-Running Task",
		"Demonstrates transparent polling of long-running MCP tool calls with the MCP Tasks extension.",
		"Model", demo.FoundryModel,
	)

	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	// Launch this same executable as a stdio MCP server in a child process.
	exe, err := os.Executable()
	if err != nil {
		demo.Panic(err)
	}
	cmd := exec.Command(exe, "--server")
	cmd.Stderr = os.Stderr

	session, err := mcptool.Connect(ctx, &mcp.CommandTransport{Command: cmd})
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = session.Close() }()

	// Wrap each MCP tool with task-aware behavior. A task-capable server may
	// return a task handle, while other servers can still return inline results.
	// The zero TaskOptions uses the default polling and cancellation settings.
	tools, err := mcptool.ListToolsWithTasks(ctx, session, mcptool.TaskOptions{})
	if err != nil {
		demo.Panic(err)
	}

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You answer data-analysis questions by invoking the available tools. Always invoke a tool when one matches the request.",
			Config: agent.Config{
				Name:        "DatasetAnalyst",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
				Tools:       tools,
			},
		},
	)

	// The tool takes ~15s; the run blocks while the tool polls the task.
	start := time.Now()
	resp, err := a.RunText(ctx, prompt).Collect()
	demo.Response(resp, err)
	fmt.Printf("(Completed after %.1fs.)\n\n", time.Since(start).Seconds())

	// Updates only begin to arrive after the task completes, since the model
	// needs the tool result before it can produce its final answer.
	start = time.Now()
	for update, err := range a.RunText(ctx, prompt, agent.Stream(true)) {
		demo.Response(update, err)
	}
	fmt.Printf("(Streaming completed after %.1fs.)\n", time.Since(start).Seconds())
}

func runServer() {
	// Stdout carries the JSON-RPC channel, so diagnostics must go to stderr.
	srv, _ := newDatasetServer(15 * time.Second)
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "dataset MCP server failed: %v\n", err)
		os.Exit(1)
	}
}
