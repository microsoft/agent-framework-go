// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to expose an Agent as an MCP tool.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run "npx @modelcontextprotocol/inspector go run ." to connect to this MCP server.

func main() {
	// Create the agent with the same configuration as the C# example.
	agent := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, chatagent.Config{
		Name:         "Joker",
		Description:  "An agent that tells jokes.",
		Instructions: "You are good at telling jokes, and you always start each joke with 'Aye aye, captain!'.",
	})

	// Create an MCP server with the agent exposed as a tool.
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "agent-mcp-server",
		Version: "1.0.0",
	}, nil)

	// Register the agent as an MCP tool.
	mcptool.AddTool(srv, agent.AsFuncTool())

	// Run the MCP server with StdIO transport.
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		panic(err)
	}
}
