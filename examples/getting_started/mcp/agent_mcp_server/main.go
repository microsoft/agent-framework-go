// Copyright (c) Microsoft. All rights reserved.

// This sample shows how to create and use a simple Agent with tools from an MCP Server.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/openai"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

	// Create the Agent with MCP tools
	// In Go, we configure tools as default options that will be used for all runs
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		Name:         "DocsAgent",
		Instructions: "You are a helpful assistant that can help with microsoft documentation questions.",
		ChatOptions: &chatagent.ChatOptions{
			Tools: tools,
		},
	})

	// Invoke the agent and output the text result.
	fmt.Println(agent.RunText(ctx, a, "How to create an Azure storage account using az cli?"))

}
