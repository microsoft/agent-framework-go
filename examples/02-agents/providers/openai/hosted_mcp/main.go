// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"os"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
	"github.com/openai/openai-go/v3"
)

// learnMCPEndpoint is a public, remote MCP server hosted by Microsoft. The
// OpenAI Responses service connects to it directly (server-side hosted MCP),
// so no local MCP client or transport is needed.
const learnMCPEndpoint = "https://learn.microsoft.com/api/mcp"

var logger = demo.NewLogger(
	"OpenAI Hosted MCP",
	"Demonstrates the hosted MCP server tool with an OpenAI Responses agent.",
	"Model", "gpt-4o-mini",
)

func main() {
	// The hosted MCP call is executed by the OpenAI Responses service, which
	// requires an API key. Skip the live run when it is not configured.
	if os.Getenv("OPENAI_API_KEY") == "" {
		demo.Assistant("OPENAI_API_KEY is not set; skipping the live hosted MCP run.")
		return
	}

	a := openaiprovider.NewResponsesAgent(
		openai.NewClient(),
		openaiprovider.AgentConfig{
			Model:        "gpt-4o-mini",
			Instructions: "You are a helpful assistant that can answer Microsoft documentation questions.",
			Config: agent.Config{
				Name:        "DocsAgent",
				Middlewares: []agent.Middleware{logger},
				// Attach a hosted MCP server. The service connects to
				// ServerAddress on its own; AllowedTools restricts which of the
				// server's tools the model may invoke.
				Tools: []tool.Tool{
					&hostedtool.MCPServer{
						ServerName:    "microsoft_learn",
						ServerAddress: learnMCPEndpoint,
						AllowedTools:  []string{"microsoft_docs_search"},
					},
				},
			},
		},
	)

	resp, err := a.RunText(context.Background(), "How do I create an Azure storage account using the az CLI?").Collect()
	demo.Response(resp, err)
	if err != nil || resp == nil {
		return
	}

	// Inspect the response contents for records of the hosted MCP interaction.
	for content := range resp.Contents() {
		switch c := content.(type) {
		case *message.MCPServerToolCallContent:
			demo.Assistant("MCP tool call:")
			demo.Assistant("  Server: " + c.ServerName)
			demo.Assistant("  Tool: " + c.Name)
			if c.Arguments != "" {
				demo.Assistant("  Arguments: " + c.Arguments)
			}
		case *message.ToolApprovalRequestContent:
			// Hosted MCP servers may require approval before a tool runs. The
			// request carries the pending MCP call for inspection.
			if call, ok := c.ToolCall.(*message.MCPServerToolCallContent); ok {
				demo.Assistant("MCP approval requested:")
				demo.Assistant("  Server: " + call.ServerName)
				demo.Assistant("  Tool: " + call.Name)
			}
		}
	}
}
