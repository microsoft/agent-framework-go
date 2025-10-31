// Copyright (c) Microsoft. All rights reserved.

package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/microsoft/agent-framework/go/agent"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ agent.LoaderTool = (*HTTPTool)(nil)
var _ agent.InitTool = (*HTTPTool)(nil)

// HTTPTool connects to an MCP server via HTTP/SSE (Server-Sent Events).
type HTTPTool struct {
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
func (t HTTPTool) ToolInfo() (name string, description string) {
	return "mcp_http", fmt.Sprintf("MCP connection via HTTP to %s", t.URL)
}

func (t HTTPTool) Init(ctx context.Context) error {
	return t.connect(ctx)
}

func (t HTTPTool) LoadTools(ctx context.Context) ([]agent.Tool, error) {
	return t.tool.loadTools(ctx)
}

// connect establishes a connection to the MCP server via HTTP.
func (t HTTPTool) connect(ctx context.Context) error {
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

// NewHTTPTool creates a new MCP tool that connects via HTTP.
func NewHTTPTool(url string) HTTPTool {
	return HTTPTool{
		URL:  url,
		tool: new(baseTool),
	}
}
