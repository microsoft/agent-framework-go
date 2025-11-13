// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"fmt"

	"github.com/microsoft/agent-framework/go/message"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpContentToAgentContent converts MCP content types to agent framework content types.
func mcpContentToAgentContent(mcpContents []mcpsdk.Content) []message.Content {
	if len(mcpContents) == 0 {
		return nil
	}

	result := make([]message.Content, 0, len(mcpContents))

	for _, c := range mcpContents {
		switch c := c.(type) {
		case *mcpsdk.TextContent:
			result = append(result, &message.TextContent{
				Text: c.Text,
			})

		case *mcpsdk.EmbeddedResource:
			// Handle embedded resources
			if c.Resource.Text != "" {
				result = append(result, &message.TextContent{
					Text: c.Resource.Text,
				})
			} else {
				result = append(result, &message.DataContent{
					Data:      c.Resource.Blob,
					MediaType: c.Resource.MIMEType,
					URI:       c.Resource.URI,
				})
			}

		default:
			// Unknown content type - convert to text
			result = append(result, &message.TextContent{
				Text: fmt.Sprintf("[Unknown MCP content type: %T]", c),
			})
		}
	}

	return result
}
