// Copyright (c) Microsoft. All rights reserved.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"runtime"

	"github.com/microsoft/agent-framework/go/pkg/internal/exp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var _ Tool = (*HTTPTool)(nil)
var _ exp.LoaderTool = (*HTTPTool)(nil)
var _ exp.InitTool = (*HTTPTool)(nil)

// HTTPTool connects to an MCP server via HTTP/SSE (Server-Sent Events).
type HTTPTool struct {
	baseTool

	// URL is the endpoint of the MCP server.
	URL string

	// Headers are additional HTTP headers to include in requests.
	Headers map[string]string

	// HTTPClient is the HTTP client to use for requests.
	// If nil, http.DefaultClient is used.
	HTTPClient *http.Client

	// Name is the client implementation name.
	Name string

	// Version is the client implementation version.
	Version string
}

// ToolInfo implements the agent.Tool interface.
func (t *HTTPTool) ToolInfo() (name string, description string) {
	return "mcp_http", fmt.Sprintf("MCP connection via HTTP to %s", t.URL)
}

func (t *HTTPTool) Init(ctx context.Context) error {
	return t.connect(ctx)
}

// connect establishes a connection to the MCP server via HTTP.
func (t *HTTPTool) connect(ctx context.Context) error {
	// Create the transport
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint:   t.URL,
		HTTPClient: t.HTTPClient,
	}

	// Create implementation info
	impl := &mcpsdk.Implementation{
		Name:    t.Name,
		Version: t.Version,
	}
	if impl.Name == "" {
		impl.Name = "agent-framework-mcp-client"
	}
	if impl.Version == "" {
		impl.Version = "1.0.0"
	}

	// Connect using the base tool
	return t.baseTool.connect(ctx, transport, impl)
}

// NewHTTPTool creates a new MCP tool that connects via HTTP.
func NewHTTPTool(url string, headers map[string]string, httpClient *http.Client, samplingCallback SamplingCallback) *HTTPTool {
	t := &HTTPTool{
		URL:        url,
		Headers:    headers,
		HTTPClient: httpClient,
		baseTool: baseTool{
			samplingCallback: samplingCallback,
		},
	}
	runtime.AddCleanup(t, func(client *mcpsdk.ClientSession) { client.Close() }, t.baseTool.session)
	return t
}
