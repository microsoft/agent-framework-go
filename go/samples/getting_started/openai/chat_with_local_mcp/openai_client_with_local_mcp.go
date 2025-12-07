// Copyright (c) Microsoft. All rights reserved.

// OpenAI Chat Client with Local MCP Example
//
// This sample demonstrates integrating Model Context Protocol (MCP) tools with
// OpenAI Chat Client for extended functionality and external service access.
package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	fmt.Println("=== OpenAI Chat Agent with MCP Tools Examples ===")

	// Run both examples
	mcpToolsOnAgentLevel()

	mcpToolsOnRunLevel()
}

// mcpToolsOnAgentLevel demonstrates tools defined when creating the agent.
// The agent can use these tools for any query during its lifetime.
func mcpToolsOnAgentLevel() {
	fmt.Println("=== Tools Defined on Agent Level ===")

	// Create MCP HTTP tool for Microsoft Learn
	session := must(mcptool.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: "https://learn.microsoft.com/api/mcp",
	}))
	defer session.Close()
	tools := must(mcptool.ListTools(context.Background(), session))

	// Create the OpenAI agent with MCP tools
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

	ctx := context.Background()

	// First query - uses the tools defined at agent creation
	const query1 = "How to create an Azure storage account using az cli?"
	fmt.Println("User: ", query1)
	fmt.Println(a.Identity().Name(), ": ", must(agent.RunText(ctx, a, query1)))

	fmt.Println("\n=======================================")

	// Second query
	const query2 = "What is Microsoft Agent Framework?"
	fmt.Println("User: ", query2)
	fmt.Println(a.Identity().Name(), ": ", must(agent.RunText(ctx, a, query2)))
}

// mcpToolsOnRunLevel demonstrates MCP tools defined when running the agent.
func mcpToolsOnRunLevel() {
	fmt.Println("=== Tools Defined on Run Level ===")

	// Create MCP HTTP tool for Microsoft Learn
	session := must(mcptool.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: "https://learn.microsoft.com/api/mcp",
	}))
	defer session.Close()
	tools := must(mcptool.ListTools(context.Background(), session))

	// Create the OpenAI agent
	a := openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		Name:         "DocsAgent",
		Instructions: "You are a helpful assistant that can help with microsoft documentation questions.",
	})

	ctx := context.Background()
	opts := agent.RunOptions{
		Options: &chatagent.ChatOptions{
			Tools:    tools,
			ToolMode: tool.ToolModeAuto,
		},
	}

	// First query
	query1 := "How to create an Azure storage account using az cli?"
	fmt.Println("User: ", query1)
	fmt.Println(a.Identity().Name(), ": ", must(agent.Run(ctx, a, opts, message.NewText(query1))))

	fmt.Println("\n=======================================")

	// Second query
	query2 := "What is Microsoft Agent Framework?"
	fmt.Println("User: ", query2)
	fmt.Println(a.Identity().Name(), ": ", must(agent.Run(ctx, a, opts, message.NewText(query2))))
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
