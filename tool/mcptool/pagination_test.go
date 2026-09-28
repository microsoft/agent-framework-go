// Copyright (c) Microsoft. All rights reserved.

package mcptool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newPaginatedToolServer(names ...string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, &mcp.ServerOptions{PageSize: 1})
	for _, name := range names {
		server.AddTool(&mcp.Tool{
			Name: name, InputSchema: map[string]any{"type": "object"},
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name}}}, nil
		})
	}
	return server
}

func TestListToolsPagination(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
	}{
		{name: "empty"},
		{name: "single page", names: []string{"first"}},
		{name: "multiple pages", names: []string{"first", "second", "third"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			session := connectInMemory(t, ctx, newPaginatedToolServer(tc.names...))
			tools, err := mcptool.ListTools(ctx, session)
			if err != nil {
				t.Fatalf("ListTools() error = %v", err)
			}
			if len(tools) != len(tc.names) {
				t.Fatalf("ListTools() returned %d tools, want %d", len(tools), len(tc.names))
			}
			for i, listed := range tools {
				if listed.Name() != tc.names[i] {
					t.Fatalf("tool %d name = %q, want %q", i, listed.Name(), tc.names[i])
				}
				callable, ok := listed.(tool.FuncTool)
				if !ok {
					t.Fatalf("tool %d is %T, want tool.FuncTool", i, listed)
				}
				result, err := callable.Call(ctx, `{}`)
				if err != nil {
					t.Fatalf("Call() error = %v", err)
				}
				contents, ok := result.(message.Contents)
				if !ok || len(contents) != 1 {
					t.Fatalf("Call() result = %#v, want one content item", result)
				}
				text, ok := contents[0].(*message.TextContent)
				if !ok || text.Text != tc.names[i] {
					t.Fatalf("Call() content = %#v, want text %q", contents[0], tc.names[i])
				}
			}
		})
	}
}

func TestListToolsRejectsNormalizedNameCollisionAcrossPages(t *testing.T) {
	ctx := t.Context()
	session := connectInMemory(t, ctx, newPaginatedToolServer("search a", "search/a"))
	tools, err := mcptool.ListTools(ctx, session)
	if err == nil || !strings.Contains(err.Error(), "normalized MCP tool name collision") || !strings.Contains(err.Error(), "search-a") {
		t.Fatalf("ListTools() error = %v, want normalized name collision for search-a", err)
	}
	if tools != nil {
		t.Fatalf("ListTools() returned partial tools: %v", tools)
	}
}

func TestListToolsLaterPageError(t *testing.T) {
	ctx := t.Context()
	server := newPaginatedToolServer("first", "second")
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" {
				if req := req.(*mcp.ListToolsRequest); req.Params != nil && req.Params.Cursor != "" {
					return nil, errors.New("second page unavailable")
				}
			}
			return next(ctx, method, req)
		}
	})
	session := connectInMemory(t, ctx, server)
	tools, err := mcptool.ListTools(ctx, session)
	if err == nil || !strings.Contains(err.Error(), "failed to list tools:") || !strings.Contains(err.Error(), "second page unavailable") {
		t.Fatalf("ListTools() error = %v, want wrapped later-page error", err)
	}
	if tools != nil {
		t.Fatalf("ListTools() returned partial tools: %v", tools)
	}
}
