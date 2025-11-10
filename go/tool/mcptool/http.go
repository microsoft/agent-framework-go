// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"context"
	"fmt"
	"net/http"

	"github.com/microsoft/agent-framework/go/tool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ tool.LoaderTool = (*HTTP)(nil)
var _ tool.InitTool = (*HTTP)(nil)

// HTTP connects to an MCP server via HTTP/SSE (Server-Sent Events).
type HTTP struct {
	tool *baseTool

	// URL is the endpoint of the MCP server.
	URL string

	// HTTPClient is the HTTP client to use for requests.
	// If nil, http.DefaultClient is used.
	HTTPClient *http.Client

	// Name is the client implementation name.
	Name string

	// Version is the client implementation version.
	Version string
}

// ToolInfo implements the agent.Tool interface.
func (t HTTP) ToolInfo() (name string, description string) {
	return "mcp_http", fmt.Sprintf("MCP connection via HTTP to %s", t.URL)
}

func (t HTTP) Init(ctx context.Context) error {
	return t.connect(ctx)
}

func (t HTTP) LoadTools(ctx context.Context) ([]tool.Tool, error) {
	return t.tool.loadTools(ctx)
}

// connect establishes a connection to the MCP server via HTTP.
func (t HTTP) connect(ctx context.Context) error {
	// Create the transport
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint:   t.URL,
		HTTPClient: t.HTTPClient,
	}
	// Connect using the base tool
	return t.tool.connect(ctx, transport, &mcpsdk.Implementation{
		Name:    t.Name,
		Version: t.Version,
	})
}

// NewHTTP creates a new MCP tool that connects via HTTP.
func NewHTTP(url string) HTTP {
	return HTTP{
		URL:  url,
		tool: new(baseTool),
	}
}
