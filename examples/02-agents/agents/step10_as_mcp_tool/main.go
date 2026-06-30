// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to expose an Agent as an MCP tool.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool/agenttool"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var (
	endpoint   = os.Getenv("AZURE_OPENAI_ENDPOINT")
	apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")
	deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
)

// Run "npx @modelcontextprotocol/inspector go run ." to connect to this MCP server.

func main() {
	// Get Azure token credential for authentication with Azure OpenAI.
	token := demo.AzureTokenCredential()

	// Create Azure OpenAI agent with the same configuration as the C# example.
	agent := openaiprovider.NewAgent(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaiprovider.AgentConfig{
			Model:        deployment,
			Instructions: "You are good at telling jokes, and you always start each joke with 'Aye aye, captain!'.",
			Config: agent.Config{
				Name:        "Joker",
				Description: "An agent that tells jokes.",
			},
		},
	)

	// Create an MCP server with the agent exposed as a tool.
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "agent-mcp-server",
		Version: "1.0.0",
	}, nil)

	// Register the agent as an MCP tool.
	mcptool.AddTool(srv, agenttool.New(agent, agenttool.Config{}))

	// Run the MCP server with StdIO transport.
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		demo.Panic(err)
	}
}
