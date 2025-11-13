// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/content"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpContentToAgentContent converts MCP content types to agent framework content types.
func mcpContentToAgentContent(mcpContents []mcpsdk.Content) []content.Content {
	if len(mcpContents) == 0 {
		return nil
	}

	result := make([]content.Content, 0, len(mcpContents))

	for _, c := range mcpContents {
		switch c := c.(type) {
		case *mcpsdk.TextContent:
			result = append(result, &content.Text{
				Text: c.Text,
			})

		case *mcpsdk.EmbeddedResource:
			// Handle embedded resources
			if c.Resource.Text != "" {
				result = append(result, &content.Text{
					Text: c.Resource.Text,
				})
			} else {
				result = append(result, &content.Data{
					Data:      c.Resource.Blob,
					MediaType: c.Resource.MIMEType,
					URI:       c.Resource.URI,
				})
			}

		default:
			// Unknown content type - convert to text
			result = append(result, &content.Text{
				Text: fmt.Sprintf("[Unknown MCP content type: %T]", c),
			})
		}
	}

	return result
}
