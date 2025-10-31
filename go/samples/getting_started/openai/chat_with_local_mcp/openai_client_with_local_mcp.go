// Copyright (c) Microsoft. All rights reserved.

// OpenAI Chat Client with Local MCP Example
//
// This sample demonstrates integrating Model Context Protocol (MCP) tools with
// OpenAI Chat Client for extended functionality and external service access.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/mcp"
	"github.com/microsoft/agent-framework/go/openai"
)

func main() {
	fmt.Println("=== OpenAI Chat Agent with MCP Tools Examples ===")

	// Run both examples
	if err := mcpToolsOnAgentLevel(); err != nil {
		log.Fatalf("Agent level example failed: %v", err)
	}

	if err := mcpToolsOnRunLevel(); err != nil {
		log.Fatalf("Run level example failed: %v", err)
	}
}

// mcpToolsOnAgentLevel demonstrates tools defined when creating the agent.
// The agent can use these tools for any query during its lifetime.
func mcpToolsOnAgentLevel() error {
	fmt.Println("=== Tools Defined on Agent Level ===")

	// Create MCP HTTP tool for Microsoft Learn
	mcpTool := mcp.NewHTTPTool("https://learn.microsoft.com/api/mcp")

	ctx := context.Background()

	// Create the OpenAI agent with MCP tools
	// In Go, we configure tools as default options that will be used for all runs
	client := openai.NewChatClient(
		openai.AgentConfig{
			Model: "gpt-4o-mini",
		},
	)
	ag := agent.New(client, &agent.Config{
		Name:               "DocsAgent",
		SystemInstructions: "You are a helpful assistant that can help with microsoft documentation questions.",
	}, &agent.RunOptions{
		Tools:    []agent.Tool{mcpTool},
		ToolMode: agent.ToolModeAuto,
	})

	// First query - uses the tools defined at agent creation
	const query1 = "How to create an Azure storage account using az cli?"
	fmt.Printf("User: %s\n", query1)
	result1, err := ag.RunText(ctx, query1)
	if err != nil {
		return fmt.Errorf("agent run failed: %w", err)
	}
	fmt.Printf("%s: %s\n\n", ag.Name(), result1.Text())

	fmt.Println("\n=======================================")

	// Second query
	const query2 = "What is Microsoft Agent Framework?"
	fmt.Printf("User: %s\n", query2)
	result2, err := ag.RunText(ctx, query2)
	if err != nil {
		return fmt.Errorf("agent run failed: %w", err)
	}
	fmt.Printf("%s: %s\n\n", ag.Name(), result2.Text())

	return nil
}

// mcpToolsOnRunLevel demonstrates MCP tools defined when running the agent.
func mcpToolsOnRunLevel() error {
	fmt.Println("=== Tools Defined on Run Level ===")

	// Create MCP HTTP tool for Microsoft Learn
	mcpServer := mcp.NewHTTPTool("https://learn.microsoft.com/api/mcp")

	ctx := context.Background()

	// Create the OpenAI agent
	client := openai.NewChatClient(
		openai.AgentConfig{
			Model: "gpt-4o-mini",
		},
	)
	ag := agent.New(client, &agent.Config{
		Name:               "DocsAgent",
		SystemInstructions: "You are a helpful assistant that can help with microsoft documentation questions.",
	}, nil)

	// First query
	query1 := "How to create an Azure storage account using az cli?"
	fmt.Printf("User: %s\n", query1)
	result1, err := ag.Run(
		ctx,
		nil,
		&agent.RunOptions{
			Tools:    []agent.Tool{mcpServer},
			ToolMode: agent.ToolModeAuto,
		},
		agent.NewTextMessage(query1),
	)
	if err != nil {
		return fmt.Errorf("agent run failed: %w", err)
	}
	fmt.Printf("%s: %s\n\n", ag.Name(), result1.Text())

	fmt.Print("\n=======================================\n")

	// Second query
	query2 := "What is Microsoft Agent Framework?"
	fmt.Printf("User: %s\n", query2)
	result2, err := ag.Run(
		ctx,
		nil,
		&agent.RunOptions{
			Tools:    []agent.Tool{mcpServer},
			ToolMode: agent.ToolModeAuto,
		},
		agent.NewTextMessage(query2),
	)
	if err != nil {
		return fmt.Errorf("agent run failed: %w", err)
	}
	fmt.Printf("%s: %s\n\n", ag.Name(), result2.Text())

	return nil
}
