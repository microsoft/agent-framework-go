// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const learnMCPEndpoint = "https://learn.microsoft.com/api/mcp"

var logger = demo.NewLogger(
	"Foundry Local MCP",
	"Demonstrates wrapping locally-resolved MCP tools with custom behavior.",
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

	mcpTools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		demo.Panic(err)
	}
	wrappedTools := make([]tool.Tool, 0, len(mcpTools))
	for _, mcpTool := range mcpTools {
		if funcTool, ok := mcpTool.(tool.FuncTool); ok {
			wrappedTools = append(wrappedTools, loggingFuncTool{FuncTool: funcTool})
			continue
		}
		wrappedTools = append(wrappedTools, mcpTool)
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
				Tools:       wrappedTools,
			},
		},
	)

	resp, err := a.RunText(ctx, "How does one create an Azure storage account using az cli?").Collect()
	demo.Response(resp, err)
}

type loggingFuncTool struct {
	tool.FuncTool
}

func (t loggingFuncTool) Call(ctx context.Context, args string) (any, error) {
	fmt.Printf("  >> [LOCAL MCP] Invoking tool %q locally...\n", t.Name())
	return t.FuncTool.Call(ctx, args)
}
