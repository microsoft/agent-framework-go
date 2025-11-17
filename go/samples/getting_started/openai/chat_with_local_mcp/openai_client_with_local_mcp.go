// Copyright (c) Microsoft. All rights reserved.

// OpenAI Chat Client with Local MCP Example
//
// This sample demonstrates integrating Model Context Protocol (MCP) tools with
// OpenAI Chat Client for extended functionality and external service access.
package main

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/mcptool"
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
	mcpTool := mcptool.NewHTTP("https://learn.microsoft.com/api/mcp")

	// Create the OpenAI agent with MCP tools
	// In Go, we configure tools as default options that will be used for all runs
	ag := openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-4o-mini",
		Name:               "DocsAgent",
		SystemInstructions: "You are a helpful assistant that can help with microsoft documentation questions.",
		Opts: &agent.RunOptions{
			Tools: []tool.Tool{mcpTool},
		},
	})

	// First query - uses the tools defined at agent creation
	const query1 = "How to create an Azure storage account using az cli?"
	fmt.Println("User: ", query1)
	fmt.Println(ag.Name(), ": ", must(ag.RunText(nil, query1)))

	fmt.Println("\n=======================================")

	// Second query
	const query2 = "What is Microsoft Agent Framework?"
	fmt.Println("User: ", query2)
	fmt.Println(ag.Name(), ": ", must(ag.RunText(nil, query2)))
}

// mcpToolsOnRunLevel demonstrates MCP tools defined when running the agent.
func mcpToolsOnRunLevel() {
	fmt.Println("=== Tools Defined on Run Level ===")

	// Create MCP HTTP tool for Microsoft Learn
	mcpServer := mcptool.NewHTTP("https://learn.microsoft.com/api/mcp")

	// Create the OpenAI agent
	ag := openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-4o-mini",
		Name:               "DocsAgent",
		SystemInstructions: "You are a helpful assistant that can help with microsoft documentation questions.",
	})

	ctx := &agent.RunContext{
		Options: &agent.RunOptions{
			Tools:    []tool.Tool{mcpServer},
			ToolMode: tool.ToolModeAuto,
		},
	}

	// First query
	query1 := "How to create an Azure storage account using az cli?"
	fmt.Println("User: ", query1)
	fmt.Println(ag.Name(), ": ", must(ag.Run(ctx, message.NewText(query1))))

	fmt.Println("\n=======================================")

	// Second query
	query2 := "What is Microsoft Agent Framework?"
	fmt.Println("User: ", query2)
	fmt.Println(ag.Name(), ": ", must(ag.Run(ctx, message.NewText(query2))))
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
