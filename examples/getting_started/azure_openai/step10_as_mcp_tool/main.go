// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to expose an Agent as an MCP tool.

package main

import (
	"context"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichat"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = os.Getenv("AZURE_OPENAI_API_VERSION")
var apiKey = os.Getenv("AZURE_OPENAI_API_KEY")

// Run "npx @modelcontextprotocol/inspector go run ." to connect to this MCP server.

func main() {
	// Create Azure OpenAI agent with the same configuration as the C# example.
	agent := openaichat.NewAgent(openaichat.Config{
		Client: openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithAPIKey(apiKey),
		),
		Model: deployment,
		Agent: agent.Config{
			Name:         "Joker",
			Description:  "An agent that tells jokes.",
			Instructions: "You are good at telling jokes, and you always start each joke with 'Aye aye, captain!'.",
		},
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
