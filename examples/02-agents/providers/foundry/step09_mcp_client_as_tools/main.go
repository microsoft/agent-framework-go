// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const learnMCPEndpoint = "https://learn.microsoft.com/api/mcp"

var logger = demo.NewLogger(
	"Foundry MCP Client Tools",
	"Demonstrates using remote MCP tools with a Foundry agent.",
	"Model", demo.FoundryModel,
)

func main() {
	ctx := context.Background()
	token := demo.FoundryTokenCredential()

	session, err := mcptool.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: learnMCPEndpoint})
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = session.Close() }()

	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		demo.Panic(err)
	}

	a := foundryprovider.NewAgent(
		demo.FoundryProjectEndpoint,
		token,
		foundryprovider.ModelDeployment(demo.FoundryModel),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant that can help with Microsoft documentation questions.",
			Config: agent.Config{
				Name:        "DocsAgent",
				Middlewares: []agent.Middleware{logger},
				Tools:       tools,
			},
		},
	)

	resp, err := a.RunText(ctx, "How does one create an Azure storage account using az cli?").Collect()
	demo.Response(resp, err)
}
