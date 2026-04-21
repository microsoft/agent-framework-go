// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to expose an Agent as an MCP tool.

package main

import (
	"cmp"
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/openaichatagent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
)

var deployment = cmp.Or(os.Getenv("AZURE_OPENAI_DEPLOYMENT_NAME"), "gpt-4o-mini")
var endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
var apiVersion = cmp.Or(os.Getenv("AZURE_OPENAI_API_VERSION"), "2025-01-01-preview")

// Run "npx @modelcontextprotocol/inspector go run ." to connect to this MCP server.

func main() {
	demo.CheckAzureEndpoint(endpoint)
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	// Create Azure OpenAI agent with the same configuration as the C# example.
	agent := openaichatagent.New(
		openai.NewClient(
			azure.WithEndpoint(endpoint, apiVersion),
			azure.WithTokenCredential(token),
		),
		openaichatagent.Config{
			Model: deployment,
			Config: agent.Config{
				Name:         "Joker",
				Description:  "An agent that tells jokes.",
				Instructions: "You are good at telling jokes, and you always start each joke with 'Aye aye, captain!'.",
			},
		},
	)

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
