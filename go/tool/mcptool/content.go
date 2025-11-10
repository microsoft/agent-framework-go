// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/agent"
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
