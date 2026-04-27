// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
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
	defer func() { _ = session.Close() }()

	// Retrieve the list of tools available on the Microsoft Learn server
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		panic(err)
	}

	// Create the Agent with MCP tools
	a := openaichatagent.New(
		openai.NewClient(),
		openaichatagent.Config{
			Model: "gpt-4o-mini",
			Config: agent.Config{
				Name:         "DocsAgent",
				Instructions: "You are a helpful assistant that can help with microsoft documentation questions.",
				Middlewares:  []agent.Middleware{logger}, // for logging agent interactions
				Tools:        tools,
			},
		},
	)

	// Invoke the agent and output the text result.
	resp, err := a.RunText(ctx, "How to create an Azure storage account using az cli?").Collect()
	demo.Response(resp, err)
}
