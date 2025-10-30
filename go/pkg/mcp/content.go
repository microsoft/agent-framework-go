// Copyright (c) Microsoft. All rights reserved.

package mcp

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpContentToAgentContent converts MCP content types to agent framework content types.
func mcpContentToAgentContent(mcpContents []mcpsdk.Content) []agent.Content {
	if len(mcpContents) == 0 {
		return nil
	}

	result := make([]agent.Content, 0, len(mcpContents))

	for _, content := range mcpContents {
		switch c := content.(type) {
		case *mcpsdk.TextContent:
			result = append(result, &agent.TextContent{
				Text: c.Text,
			})

		case *mcpsdk.EmbeddedResource:
			// Handle embedded resources
			if c.Resource.Text != "" {
				result = append(result, &agent.TextContent{
					Text: c.Resource.Text,
				})
			} else {
				result = append(result, &agent.DataContent{
					Data:      c.Resource.Blob,
					MediaType: c.Resource.MIMEType,
					URI:       c.Resource.URI,
				})
			}

		default:
			// Unknown content type - convert to text
			result = append(result, &agent.TextContent{
				Text: fmt.Sprintf("[Unknown MCP content type: %T]", c),
			})
		}
	}

	return result
}

// mcpPromptToAgentContent converts MCP prompt result to agent framework content.
func mcpPromptToAgentContent(promptResult *mcpsdk.GetPromptResult) []agent.Content {
	if promptResult == nil {
		return nil
	}

	result := make([]agent.Content, 0, len(promptResult.Messages))

	for _, msg := range promptResult.Messages {
		// Add role information as text prefix
		rolePrefix := ""
		switch msg.Role {
		case "user":
			rolePrefix = "[User] "
		case "assistant":
			rolePrefix = "[Assistant] "
		}

		// Convert message content
		msgContents := mcpContentToAgentContent([]mcpsdk.Content{msg.Content})

		// Add role prefix to first text content
		if len(msgContents) > 0 {
			if textContent, ok := msgContents[0].(*agent.TextContent); ok {
				textContent.Text = rolePrefix + textContent.Text
			}
		}

		result = append(result, msgContents...)
	}

	return result
}

// agentContentToMCPContent converts agent framework content to MCP content types.
// This is used when sending sampling requests to MCP servers.
func agentContentToMCPContent(agentContents []agent.Content) []mcpsdk.Content {
	if len(agentContents) == 0 {
		return nil
	}

	result := make([]mcpsdk.Content, 0, len(agentContents))

	for _, content := range agentContents {
		switch c := content.(type) {
		case *agent.TextContent:
			result = append(result, &mcpsdk.TextContent{
				Text: c.Text,
			})

		default:
			// For other content types, convert to text representation
			result = append(result, &mcpsdk.TextContent{
				Text: fmt.Sprintf("[%T content]", c),
			})
		}
	}

	return result
}

// agentMessagesToMCPMessages converts agent messages to MCP messages for sampling.
func agentMessagesToMCPMessages(messages []*agent.Message) []*mcpsdk.SamplingMessage {
	if len(messages) == 0 {
		return nil
	}

	result := make([]*mcpsdk.SamplingMessage, 0, len(messages))

	for _, msg := range messages {
		// Map agent role to MCP role
		var mcpRole mcpsdk.Role
		switch msg.Role {
		case agent.RoleUser:
			mcpRole = "user"
		case agent.RoleAssistant:
			mcpRole = "assistant"
		default:
			// For system and other roles, use user as default
			mcpRole = "user"
		}

		// Convert content
		if len(msg.Contents) > 0 {
			// MCP messages have a single content field
			// If there are multiple contents, we'll create multiple messages
			for _, content := range msg.Contents {
				mcpContent := agentContentToMCPContent([]agent.Content{content})
				if len(mcpContent) > 0 {
					result = append(result, &mcpsdk.SamplingMessage{
						Role:    mcpRole,
						Content: mcpContent[0],
					})
				}
			}
		}
	}

	return result
}
