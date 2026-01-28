// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var logger = demo.NewLogger(
	"MCP Tools",
	"Demonstrates how to create and use an Agent with tools from an MCP Server.",
	"Model", "gpt-4o-mini",
)

func main() {
	ctx := context.Background()

	// Create MCP HTTP tool for Microsoft Learn
	session, err := mcptool.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: "https://learn.microsoft.com/api/mcp",
	})
	if err != nil {
		panic(err)
	}
	defer session.Close()

	// Retrieve the list of tools available on the Microsoft Learn server
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		panic(err)
	}

	var opts []agentopt.RunOption
	for _, t := range tools {
		opts = append(opts, agentopt.Tool(t))
	}
	opts = append(opts, middleware.With(logger)) // for logging agent interactions

	// Create the Agent with MCP tools
	// In Go, we configure tools as default options that will be used for all runs
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Config{
		Name:         "DocsAgent",
		Instructions: "You are a helpful assistant that can help with microsoft documentation questions.",
		RunOptions:   opts,
	})

	// Invoke the agent and output the text result.
	resp, err := a.RunText("How to create an Azure storage account using az cli?").Collect(ctx)
	demo.Response(resp, err)
}
