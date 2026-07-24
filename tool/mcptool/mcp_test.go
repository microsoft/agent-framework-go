// Copyright (c) Microsoft. All rights reserved.

package mcptool_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/mcptool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type stubFuncTool struct {
	name         string
	description  string
	schema       any
	returnSchema any
	call         func(context.Context, string) (any, error)
}

func (stub stubFuncTool) Name() string { return stub.name }

func (stub stubFuncTool) Description() string { return stub.description }

func (stub stubFuncTool) Schema() any { return stub.schema }

func (stub stubFuncTool) ReturnSchema() any { return stub.returnSchema }

func (stub stubFuncTool) Call(ctx context.Context, args string) (any, error) {
	return stub.call(ctx, args)
}

func TestCallPreservesMCPErrorResult(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "fail",
		Description: "returns an MCP tool error",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Meta: mcp.Meta{"requestId": "req-1"},
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: "friendly failure",
					Meta: mcp.Meta{"contentId": "content-1"},
				},
			},
			StructuredContent: map[string]any{"code": "bad_input"},
			IsError:           true,
		}, nil
	})

	session := connectInMemory(t, ctx, server)
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected one tool, got %d", len(tools))
	}
	funcTool, ok := tools[0].(tool.FuncTool)
	if !ok {
		t.Fatalf("listed tool is %T, want tool.FuncTool", tools[0])
	}

	result, err := funcTool.Call(ctx, `{}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	contents, ok := result.([]message.Content)
	if !ok {
		t.Fatalf("Call() result is %T, want []message.Content", result)
	}
	if len(contents) != 1 {
		t.Fatalf("expected one content item, got %d", len(contents))
	}
	text, ok := contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *message.TextContent", contents[0])
	}
	if !strings.Contains(text.Text, "friendly failure") || !strings.Contains(text.Text, "bad_input") {
		t.Fatalf("text = %q, want serialized MCP result with failure details", text.Text)
	}

	rawResult, ok := text.Header().RawRepresentation.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("RawRepresentation is %T, want *mcp.CallToolResult", text.Header().RawRepresentation)
	}
	if !rawResult.IsError {
		t.Fatal("raw result IsError = false, want true")
	}
	structured := mustMap(t, rawResult.StructuredContent)
	if structured["code"] != "bad_input" {
		t.Fatalf("raw structured content = %#v, want code bad_input", structured)
	}
	if rawResult.Meta["requestId"] != "req-1" {
		t.Fatalf("raw result meta = %#v, want requestId req-1", rawResult.Meta)
	}
	rawText := rawResult.Content[0].(*mcp.TextContent)
	if rawText.Meta["contentId"] != "content-1" {
		t.Fatalf("raw text meta = %#v, want contentId content-1", rawText.Meta)
	}
}

func TestCallConvertsMCPContentTypes(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "content",
		Description: "returns MCP content types",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "hello"},
				&mcp.ImageContent{Data: []byte("image-bytes"), MIMEType: "image/png"},
				&mcp.AudioContent{Data: []byte("audio-bytes"), MIMEType: "audio/mpeg"},
				&mcp.ResourceLink{URI: "https://example.com/doc", Title: "Example Doc", MIMEType: "text/plain"},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file://note.txt", MIMEType: "text/plain", Text: "note text"}},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file://data.bin", MIMEType: "application/octet-stream", Blob: []byte("blob-bytes")}},
			},
		}, nil
	})

	session := connectInMemory(t, ctx, server)
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	funcTool := tools[0].(tool.FuncTool)
	result, err := funcTool.Call(ctx, `{}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	contents := result.([]message.Content)
	if len(contents) != 6 {
		t.Fatalf("expected six content items, got %d", len(contents))
	}
	if got := contents[0].(*message.TextContent).Text; got != "hello" {
		t.Fatalf("text content = %q, want hello", got)
	}
	image := contents[1].(*message.DataContent)
	if image.MediaType != "image/png" || image.Data != base64.StdEncoding.EncodeToString([]byte("image-bytes")) {
		t.Fatalf("image content = (%q, %q), want image/png with encoded bytes", image.MediaType, image.Data)
	}
	audio := contents[2].(*message.DataContent)
	if audio.MediaType != "audio/mpeg" || audio.Data != base64.StdEncoding.EncodeToString([]byte("audio-bytes")) {
		t.Fatalf("audio content = (%q, %q), want audio/mpeg with encoded bytes", audio.MediaType, audio.Data)
	}
	uri := contents[3].(*message.URIContent)
	if uri.URI != "https://example.com/doc" || uri.MediaType != "text/plain" {
		t.Fatalf("uri content = (%q, %q), want example doc text/plain", uri.URI, uri.MediaType)
	}
	rawLink, ok := uri.Header().RawRepresentation.(*mcp.ResourceLink)
	if !ok {
		t.Fatalf("uri RawRepresentation is %T, want *mcp.ResourceLink", uri.Header().RawRepresentation)
	}
	if rawLink.Title != "Example Doc" {
		t.Fatalf("raw resource title = %q, want Example Doc", rawLink.Title)
	}
	embeddedText := contents[4].(*message.TextContent)
	if embeddedText.Text != "note text" {
		t.Fatalf("embedded text = %q, want note text", embeddedText.Text)
	}
	rawEmbedded, ok := embeddedText.Header().RawRepresentation.(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("embedded text RawRepresentation is %T, want *mcp.EmbeddedResource", embeddedText.Header().RawRepresentation)
	}
	if rawEmbedded.Resource.URI != "file://note.txt" {
		t.Fatalf("raw embedded resource URI = %q, want file://note.txt", rawEmbedded.Resource.URI)
	}
	embeddedBlob := contents[5].(*message.DataContent)
	if embeddedBlob.MediaType != "application/octet-stream" || embeddedBlob.Name != "file://data.bin" {
		t.Fatalf("embedded blob = (%q, %q), want application/octet-stream file://data.bin", embeddedBlob.MediaType, embeddedBlob.Name)
	}
	if embeddedBlob.Data != base64.StdEncoding.EncodeToString([]byte("blob-bytes")) {
		t.Fatalf("embedded blob data = %q, want encoded blob bytes", embeddedBlob.Data)
	}
}

func TestCallConvertsMCPDataContent(t *testing.T) {
	tests := []struct {
		name      string
		content   mcp.Content
		mediaType string
		uri       string
	}{
		{
			name:      "image empty data",
			content:   &mcp.ImageContent{Data: []byte{}, MIMEType: "image/png"},
			mediaType: "image/png",
			uri:       "data:image/png;base64,",
		},
		{
			name:      "image base64 payload",
			content:   &mcp.ImageContent{Data: mustDecodeBase64(t, "iVBORw0KGgo="), MIMEType: "image/png"},
			mediaType: "image/png",
			uri:       "data:image/png;base64,iVBORw0KGgo=",
		},
		{
			name:      "image data uri",
			content:   &mcp.ImageContent{Data: []byte("data:image/jpeg;base64,/9j/4AAQ"), MIMEType: "image/jpeg"},
			mediaType: "image/jpeg",
			uri:       "data:image/jpeg;base64,/9j/4AAQ",
		},
		{
			name:      "image default mime type",
			content:   &mcp.ImageContent{Data: mustDecodeBase64(t, "iVBORw0KGgo=")},
			mediaType: "image/*",
			uri:       "data:image/*;base64,iVBORw0KGgo=",
		},
		{
			name:      "audio empty data",
			content:   &mcp.AudioContent{Data: []byte{}, MIMEType: "audio/wav"},
			mediaType: "audio/wav",
			uri:       "data:audio/wav;base64,",
		},
		{
			name:      "audio base64 payload",
			content:   &mcp.AudioContent{Data: mustDecodeBase64(t, "UklGRiQA"), MIMEType: "audio/wav"},
			mediaType: "audio/wav",
			uri:       "data:audio/wav;base64,UklGRiQA",
		},
		{
			name:      "audio data uri",
			content:   &mcp.AudioContent{Data: []byte("data:audio/mp3;base64,//uQxAAA"), MIMEType: "audio/mp3"},
			mediaType: "audio/mp3",
			uri:       "data:audio/mp3;base64,//uQxAAA",
		},
		{
			name:      "audio default mime type",
			content:   &mcp.AudioContent{Data: mustDecodeBase64(t, "UklGRiQA")},
			mediaType: "audio/*",
			uri:       "data:audio/*;base64,UklGRiQA",
		},
		{
			name: "embedded blob",
			content: &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI:      "resource://example.bin",
				MIMEType: "application/zip",
				Blob:     mustDecodeBase64(t, "UklGRiQA"),
			}},
			mediaType: "application/zip",
			uri:       "data:application/zip;base64,UklGRiQA",
		},
		{
			name: "embedded blob default mime type",
			content: &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI:  "resource://example.bin",
				Blob: mustDecodeBase64(t, "UklGRiQA"),
			}},
			mediaType: "application/octet-stream",
			uri:       "data:application/octet-stream;base64,UklGRiQA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := callSingleMCPContent(t, tt.content)
			data, ok := content.(*message.DataContent)
			if !ok {
				t.Fatalf("content is %T, want *message.DataContent", content)
			}
			if data.MediaType != tt.mediaType {
				t.Fatalf("MediaType = %q, want %q", data.MediaType, tt.mediaType)
			}
			if data.URI() != tt.uri {
				t.Fatalf("URI() = %q, want %q", data.URI(), tt.uri)
			}
		})
	}
}

func TestCallConvertsMCPEmbeddedTextResource(t *testing.T) {
	content := callSingleMCPContent(t, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
		URI:      "resource://example",
		MIMEType: "text/plain",
		Text:     "embedded text payload",
	}})
	text, ok := content.(*message.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *message.TextContent", content)
	}
	if text.Text != "embedded text payload" {
		t.Fatalf("Text = %q, want embedded text payload", text.Text)
	}
}

func TestListToolsPreservesMCPToolMetadata(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "search",
		Description: "Searches documentation.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []any{"query"},
		},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"answer": map[string]any{"type": "string"},
			},
		},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil
	})

	session := connectInMemory(t, ctx, server)
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected one tool, got %d", len(tools))
	}
	funcTool, ok := tools[0].(tool.FuncTool)
	if !ok {
		t.Fatalf("listed tool is %T, want tool.FuncTool", tools[0])
	}
	if funcTool.Name() != "search" {
		t.Fatalf("Name() = %q, want search", funcTool.Name())
	}
	if funcTool.Description() != "Searches documentation." {
		t.Fatalf("Description() = %q, want Searches documentation.", funcTool.Description())
	}

	inputSchema := mustMap(t, funcTool.Schema())
	properties := mustMap(t, inputSchema["properties"])
	query := mustMap(t, properties["query"])
	if query["type"] != "string" {
		t.Fatalf("query type = %v, want string", query["type"])
	}
	outputSchema := mustMap(t, funcTool.ReturnSchema())
	outputProperties := mustMap(t, outputSchema["properties"])
	answer := mustMap(t, outputProperties["answer"])
	if answer["type"] != "string" {
		t.Fatalf("answer type = %v, want string", answer["type"])
	}
}

func TestCallForwardsArgumentsToMCPTool(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	var captured map[string]any
	server.AddTool(&mcp.Tool{
		Name:        "echo-args",
		Description: "captures MCP arguments",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := json.Unmarshal(req.Params.Arguments, &captured); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	session := connectInMemory(t, ctx, server)
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	_, err = tools[0].(tool.FuncTool).Call(ctx, `{"query":"go sdk","limit":3}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if captured["query"] != "go sdk" || captured["limit"] != float64(3) {
		t.Fatalf("captured args = %#v, want query go sdk and limit 3", captured)
	}
}

func TestAddToolReturnsMCPToolErrorResult(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	mcptool.AddTool(server, stubFuncTool{
		name:         "explode",
		description:  "fails as a tool result",
		schema:       map[string]any{"type": "object"},
		returnSchema: map[string]any{"type": "object"},
		call: func(context.Context, string) (any, error) {
			return nil, errors.New("boom")
		},
	})

	session := connectInMemory(t, ctx, server)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "explode", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("CallTool() result IsError = false, want true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected one content item, got %d", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(text.Text, "boom") {
		t.Fatalf("error text = %q, want it to contain boom", text.Text)
	}
}

func TestAddToolForwardsArgumentsToFuncTool(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	var captured string
	mcptool.AddTool(server, stubFuncTool{
		name:         "capture-args",
		description:  "captures arguments",
		schema:       map[string]any{"type": "object"},
		returnSchema: map[string]any{"type": "object"},
		call: func(_ context.Context, args string) (any, error) {
			captured = args
			return "ok", nil
		},
	})

	session := connectInMemory(t, ctx, server)
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "capture-args", Arguments: map[string]any{"query": "go sdk", "limit": 3}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	var capturedArgs map[string]any
	if err := json.Unmarshal([]byte(captured), &capturedArgs); err != nil {
		t.Fatalf("captured args JSON error = %v; args = %q", err, captured)
	}
	if capturedArgs["query"] != "go sdk" || capturedArgs["limit"] != float64(3) {
		t.Fatalf("captured args = %#v, want query go sdk and limit 3", capturedArgs)
	}
}

func TestAddToolReturnsStructuredResult(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	mcptool.AddTool(server, stubFuncTool{
		name:         "structured",
		description:  "returns structured content",
		schema:       map[string]any{"type": "object"},
		returnSchema: map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "integer"}}},
		call: func(context.Context, string) (any, error) {
			return map[string]any{"answer": 42}, nil
		},
	})

	session := connectInMemory(t, ctx, server)
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(toolsResult.Tools) != 1 {
		t.Fatalf("expected one tool, got %d", len(toolsResult.Tools))
	}
	if toolsResult.Tools[0].OutputSchema == nil {
		t.Fatal("OutputSchema is nil, want object schema")
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "structured", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent is %T, want map[string]any", result.StructuredContent)
	}
	if structured["answer"] != float64(42) {
		t.Fatalf("structured answer = %v, want 42", structured["answer"])
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected one content item, got %d", len(result.Content))
	}
	text := result.Content[0].(*mcp.TextContent)
	if !strings.Contains(text.Text, `"answer":42`) {
		t.Fatalf("content text = %q, want JSON answer", text.Text)
	}
}

func TestAddToolReturnsErrorContentResults(t *testing.T) {
	t.Run("error content", func(t *testing.T) {
		result := callAddedTool(t, stubFuncTool{
			name:         "error-content",
			description:  "returns error content",
			schema:       map[string]any{"type": "object"},
			returnSchema: map[string]any{"type": "object"},
			call: func(context.Context, string) (any, error) {
				return &message.ErrorContent{Message: "tool failed"}, nil
			},
		})

		if !result.IsError {
			t.Fatal("IsError = false, want true")
		}
		text := result.Content[0].(*mcp.TextContent)
		if text.Text != "tool failed" {
			t.Fatalf("text = %q, want tool failed", text.Text)
		}
	})

	t.Run("function result error with content", func(t *testing.T) {
		result := callAddedTool(t, stubFuncTool{
			name:         "function-result-error",
			description:  "returns function result error",
			schema:       map[string]any{"type": "object"},
			returnSchema: map[string]any{"type": "object"},
			call: func(context.Context, string) (any, error) {
				return &message.FunctionResultContent{Result: "partial result", Error: errors.New("failed after partial result")}, nil
			},
		})

		if !result.IsError {
			t.Fatal("IsError = false, want true")
		}
		text := result.Content[0].(*mcp.TextContent)
		if text.Text != "partial result" {
			t.Fatalf("text = %q, want partial result", text.Text)
		}
	})

	t.Run("function result error without content", func(t *testing.T) {
		result := callAddedTool(t, stubFuncTool{
			name:         "function-result-error-only",
			description:  "returns function result error only",
			schema:       map[string]any{"type": "object"},
			returnSchema: map[string]any{"type": "object"},
			call: func(context.Context, string) (any, error) {
				return &message.FunctionResultContent{Error: errors.New("only failure")}, nil
			},
		})

		if !result.IsError {
			t.Fatal("IsError = false, want true")
		}
		if len(result.Content) != 1 {
			t.Fatalf("expected one content item, got %d", len(result.Content))
		}
		text := result.Content[0].(*mcp.TextContent)
		if !strings.Contains(text.Text, "only failure") {
			t.Fatalf("text = %q, want it to contain only failure", text.Text)
		}
	})
}

func TestAddToolOmitsNonObjectReturnSchema(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	mcptool.AddTool(server, stubFuncTool{
		name:         "string-output",
		description:  "returns a string",
		schema:       map[string]any{"type": "object"},
		returnSchema: map[string]any{"type": "string"},
		call: func(context.Context, string) (any, error) {
			return "ok", nil
		},
	})

	session := connectInMemory(t, ctx, server)
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(toolsResult.Tools) != 1 {
		t.Fatalf("expected one tool, got %d", len(toolsResult.Tools))
	}
	if toolsResult.Tools[0].OutputSchema != nil {
		t.Fatalf("OutputSchema = %#v, want nil for non-object schema", toolsResult.Tools[0].OutputSchema)
	}
}

func TestAddToolReturnsJSONTextResults(t *testing.T) {
	tests := []struct {
		name             string
		result           any
		wantText         string
		wantStructured   bool
		wantStructuredKV map[string]any
	}{
		{
			name:             "json object",
			result:           json.RawMessage(`{"key":"value","number":42}`),
			wantText:         `{"key":"value","number":42}`,
			wantStructured:   true,
			wantStructuredKV: map[string]any{"key": "value", "number": float64(42)},
		},
		{
			name:     "json array",
			result:   json.RawMessage(`[1,2,3,"four"]`),
			wantText: `[1,2,3,"four"]`,
		},
		{
			name:     "invalid json text",
			result:   json.RawMessage(`this is not valid json {`),
			wantText: `this is not valid json {`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callAddedTool(t, stubFuncTool{
				name:         "json-result",
				description:  "returns json-shaped text",
				schema:       map[string]any{"type": "object"},
				returnSchema: map[string]any{"type": "object"},
				call: func(context.Context, string) (any, error) {
					return tt.result, nil
				},
			})

			if len(result.Content) != 1 {
				t.Fatalf("expected one content item, got %d", len(result.Content))
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("content is %T, want *mcp.TextContent", result.Content[0])
			}
			if text.Text != tt.wantText {
				t.Fatalf("text = %q, want %q", text.Text, tt.wantText)
			}
			if !tt.wantStructured {
				if result.StructuredContent != nil {
					t.Fatalf("StructuredContent = %#v, want nil", result.StructuredContent)
				}
				return
			}

			structured := mustMap(t, result.StructuredContent)
			for key, want := range tt.wantStructuredKV {
				if structured[key] != want {
					t.Fatalf("StructuredContent[%q] = %#v, want %#v", key, structured[key], want)
				}
			}
		})
	}
}

func TestAddToolReturnsDataAndMultipleContentResults(t *testing.T) {
	t.Run("data content", func(t *testing.T) {
		result := callAddedTool(t, stubFuncTool{
			name:         "image-result",
			description:  "returns image content",
			schema:       map[string]any{"type": "object"},
			returnSchema: map[string]any{"type": "object"},
			call: func(context.Context, string) (any, error) {
				return &message.DataContent{Data: "iVBORw0KGgo=", MediaType: "image/png"}, nil
			},
		})

		if len(result.Content) != 1 {
			t.Fatalf("expected one content item, got %d", len(result.Content))
		}
		image, ok := result.Content[0].(*mcp.ImageContent)
		if !ok {
			t.Fatalf("content is %T, want *mcp.ImageContent", result.Content[0])
		}
		if image.MIMEType != "image/png" {
			t.Fatalf("MIMEType = %q, want image/png", image.MIMEType)
		}
		if string(image.Data) != string(mustDecodeBase64(t, "iVBORw0KGgo=")) {
			t.Fatalf("image data = %q, want decoded PNG bytes", string(image.Data))
		}
	})

	t.Run("multiple content", func(t *testing.T) {
		result := callAddedTool(t, stubFuncTool{
			name:         "multiple-result",
			description:  "returns multiple content blocks",
			schema:       map[string]any{"type": "object"},
			returnSchema: map[string]any{"type": "object"},
			call: func(context.Context, string) (any, error) {
				return []message.Content{
					&message.TextContent{Text: "First text"},
					&message.TextContent{Text: `{"nested": true}`},
					&message.DataContent{Data: "SUQz", MediaType: "audio/mp3"},
				}, nil
			},
		})

		if len(result.Content) != 3 {
			t.Fatalf("expected three content items, got %d", len(result.Content))
		}
		if got := result.Content[0].(*mcp.TextContent).Text; got != "First text" {
			t.Fatalf("first text = %q, want First text", got)
		}
		if got := result.Content[1].(*mcp.TextContent).Text; got != `{"nested": true}` {
			t.Fatalf("second text = %q, want JSON text", got)
		}
		audio, ok := result.Content[2].(*mcp.AudioContent)
		if !ok {
			t.Fatalf("third content is %T, want *mcp.AudioContent", result.Content[2])
		}
		if audio.MIMEType != "audio/mp3" {
			t.Fatalf("audio MIMEType = %q, want audio/mp3", audio.MIMEType)
		}
		if string(audio.Data) != "ID3" {
			t.Fatalf("audio data = %q, want ID3", string(audio.Data))
		}
	})
}

// A text-typed DataContent whose bytes are not valid UTF-8 must fall back to a
// binary Blob resource; putting invalid UTF-8 in Text corrupts it on transport.
func TestAddToolReturnsInvalidUTF8TextAsBlob(t *testing.T) {
	result := callAddedTool(t, stubFuncTool{
		name:         "bad-utf8-result",
		description:  "returns text media type with non-UTF-8 bytes",
		schema:       map[string]any{"type": "object"},
		returnSchema: map[string]any{"type": "object"},
		call: func(context.Context, string) (any, error) {
			// "//4=" is base64 for {0xff, 0xfe}, which is not valid UTF-8.
			return &message.DataContent{Name: "note.txt", Data: "//4=", MediaType: "text/plain"}, nil
		},
	})

	if len(result.Content) != 1 {
		t.Fatalf("expected one content item, got %d", len(result.Content))
	}
	embedded, ok := result.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("content is %T, want *mcp.EmbeddedResource", result.Content[0])
	}
	if embedded.Resource.Text != "" {
		t.Errorf("Resource.Text = %q, want empty (non-UTF-8 must not be placed in Text)", embedded.Resource.Text)
	}
	if len(embedded.Resource.Blob) == 0 {
		t.Error("Resource.Blob is empty, want the raw non-UTF-8 bytes")
	}
}

func TestAddToolReturnsBinaryDataAsEmbeddedResource(t *testing.T) {
	result := callAddedTool(t, stubFuncTool{
		name:         "binary-result",
		description:  "returns binary content",
		schema:       map[string]any{"type": "object"},
		returnSchema: map[string]any{"type": "object"},
		call: func(context.Context, string) (any, error) {
			return &message.DataContent{Name: "file.bin", Data: "AQIDBA==", MediaType: "application/octet-stream"}, nil
		},
	})

	if len(result.Content) != 1 {
		t.Fatalf("expected one content item, got %d", len(result.Content))
	}
	embedded, ok := result.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("content is %T, want *mcp.EmbeddedResource", result.Content[0])
	}
	if embedded.Resource.URI != "file.bin" || embedded.Resource.MIMEType != "application/octet-stream" {
		t.Fatalf("resource = (%q, %q), want file.bin application/octet-stream", embedded.Resource.URI, embedded.Resource.MIMEType)
	}
	if string(embedded.Resource.Blob) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("blob = %#v, want [1 2 3 4]", embedded.Resource.Blob)
	}
}

// A text DataContent must be surfaced as an MCP text resource (Resource.Text),
// not a binary Blob; the reverse mapping reads Resource.Text for text resources.
func TestAddToolReturnsTextDataAsTextResource(t *testing.T) {
	result := callAddedTool(t, stubFuncTool{
		name:         "text-result",
		description:  "returns text content",
		schema:       map[string]any{"type": "object"},
		returnSchema: map[string]any{"type": "object"},
		call: func(context.Context, string) (any, error) {
			// "hello" base64-encoded, with a text media type.
			return &message.DataContent{Name: "note.txt", Data: "aGVsbG8=", MediaType: "text/plain"}, nil
		},
	})

	if len(result.Content) != 1 {
		t.Fatalf("expected one content item, got %d", len(result.Content))
	}
	embedded, ok := result.Content[0].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("content is %T, want *mcp.EmbeddedResource", result.Content[0])
	}
	if embedded.Resource.Text != "hello" {
		t.Errorf("resource Text = %q, want %q", embedded.Resource.Text, "hello")
	}
	if len(embedded.Resource.Blob) != 0 {
		t.Errorf("text resource must not use Blob, got %#v", embedded.Resource.Blob)
	}
}

func TestCallReturnsEmptyAndStructuredOnlyMCPResults(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		result := callMCPResult(t, &mcp.CallToolResult{})
		if result != nil {
			t.Fatalf("result = %#v, want nil", result)
		}
	})

	t.Run("structured only", func(t *testing.T) {
		result := callMCPResult(t, &mcp.CallToolResult{StructuredContent: map[string]any{"key": "value", "number": 42}})
		contents, ok := result.([]message.Content)
		if !ok {
			t.Fatalf("result is %T, want []message.Content", result)
		}
		if len(contents) != 1 {
			t.Fatalf("expected one content item, got %d", len(contents))
		}
		text, ok := contents[0].(*message.TextContent)
		if !ok {
			t.Fatalf("content is %T, want *message.TextContent", contents[0])
		}
		if !strings.Contains(text.Text, `"key":"value"`) || !strings.Contains(text.Text, `"number":42`) {
			t.Fatalf("text = %q, want structured JSON object", text.Text)
		}
		rawResult, ok := text.Header().RawRepresentation.(*mcp.CallToolResult)
		if !ok {
			t.Fatalf("RawRepresentation is %T, want *mcp.CallToolResult", text.Header().RawRepresentation)
		}
		structured := mustMap(t, rawResult.StructuredContent)
		if structured["key"] != "value" || structured["number"] != float64(42) {
			t.Fatalf("raw structured content = %#v, want key/value and number 42", structured)
		}
	})
}

func TestCallConvertsMCPToolUseAndToolResultContent(t *testing.T) {
	result := callMCPResult(t, &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.ToolUseContent{
			ID:    "call-1",
			Name:  "calculator",
			Input: map[string]any{"x": 1},
			Meta:  mcp.Meta{"source": "assistant"},
		},
		&mcp.ToolResultContent{
			ToolUseID:         "call-1",
			Content:           []mcp.Content{&mcp.TextContent{Text: "done"}},
			StructuredContent: map[string]any{"ok": true},
			IsError:           true,
			Meta:              mcp.Meta{"resultId": "result-1"},
		},
	}})

	contents, ok := result.([]message.Content)
	if !ok {
		t.Fatalf("result is %T, want []message.Content", result)
	}
	if len(contents) != 2 {
		t.Fatalf("expected two content items, got %d", len(contents))
	}
	toolUse := contents[0].(*message.TextContent)
	if !strings.Contains(toolUse.Text, `"name":"calculator"`) || !strings.Contains(toolUse.Text, `"id":"call-1"`) {
		t.Fatalf("tool use text = %q, want calculator call JSON", toolUse.Text)
	}
	rawToolUse, ok := toolUse.Header().RawRepresentation.(*mcp.ToolUseContent)
	if !ok {
		t.Fatalf("tool use RawRepresentation is %T, want *mcp.ToolUseContent", toolUse.Header().RawRepresentation)
	}
	if rawToolUse.ID != "call-1" || rawToolUse.Name != "calculator" {
		t.Fatalf("raw tool use = %#v, want id/name", rawToolUse)
	}
	input := mustMap(t, rawToolUse.Input)
	if !sameNumber(input["x"], 1) {
		t.Fatalf("tool use input = %#v, want x 1", input)
	}
	if rawToolUse.Meta["source"] != "assistant" {
		t.Fatalf("tool use meta = %#v, want source assistant", rawToolUse.Meta)
	}

	toolResult := contents[1].(*message.TextContent)
	if toolResult.Text != "done" {
		t.Fatalf("tool result text = %q, want done", toolResult.Text)
	}
	rawToolResult, ok := toolResult.Header().RawRepresentation.(*mcp.ToolResultContent)
	if !ok {
		t.Fatalf("tool result RawRepresentation is %T, want *mcp.ToolResultContent", toolResult.Header().RawRepresentation)
	}
	if rawToolResult.ToolUseID != "call-1" || rawToolResult.IsError != true {
		t.Fatalf("raw tool result = %#v, want id and error", rawToolResult)
	}
	structured := mustMap(t, rawToolResult.StructuredContent)
	if structured["ok"] != true {
		t.Fatalf("tool result structured content = %#v, want ok true", structured)
	}
	if rawToolResult.Meta["resultId"] != "result-1" {
		t.Fatalf("tool result meta = %#v, want resultId result-1", rawToolResult.Meta)
	}
}

func TestListPromptsReturnsServerPromptDescriptors(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	server.AddPrompt(&mcp.Prompt{
		Name:        "greeting",
		Description: "Greets a person by name.",
		Arguments: []*mcp.PromptArgument{
			{Name: "name", Description: "who to greet", Required: true},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: "hi"}},
			},
		}, nil
	})

	session := connectInMemory(t, ctx, server)
	prompts, err := mcptool.ListPrompts(ctx, session)
	if err != nil {
		t.Fatalf("ListPrompts() error = %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("expected one prompt, got %d", len(prompts))
	}
	if prompts[0].Name != "greeting" {
		t.Fatalf("Name = %q, want greeting", prompts[0].Name)
	}
	if prompts[0].Description != "Greets a person by name." {
		t.Fatalf("Description = %q, want Greets a person by name.", prompts[0].Description)
	}
	if len(prompts[0].Arguments) != 1 || prompts[0].Arguments[0].Name != "name" {
		t.Fatalf("Arguments = %#v, want a single argument named name", prompts[0].Arguments)
	}
}

func TestGetPromptMaterializesAgentMessages(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	var captured map[string]string
	server.AddPrompt(&mcp.Prompt{
		Name:        "greeting",
		Description: "Greets a person by name.",
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		captured = req.Params.Arguments
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: "You are a helpful greeter."}},
				{Role: "assistant", Content: &mcp.TextContent{Text: "Hello, Ada!"}},
			},
		}, nil
	})

	session := connectInMemory(t, ctx, server)
	messages, err := mcptool.GetPrompt(ctx, session, "greeting", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatalf("GetPrompt() error = %v", err)
	}
	if captured["name"] != "Ada" {
		t.Fatalf("captured args = %#v, want name Ada", captured)
	}
	if len(messages) != 2 {
		t.Fatalf("expected two messages, got %d", len(messages))
	}

	if messages[0].Role != message.RoleUser {
		t.Fatalf("first message role = %q, want user", messages[0].Role)
	}
	firstText, ok := messages[0].Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("first content is %T, want *message.TextContent", messages[0].Contents[0])
	}
	if firstText.Text != "You are a helpful greeter." {
		t.Fatalf("first text = %q, want the greeter instruction", firstText.Text)
	}

	if messages[1].Role != message.RoleAssistant {
		t.Fatalf("second message role = %q, want assistant", messages[1].Role)
	}
	secondText, ok := messages[1].Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("second content is %T, want *message.TextContent", messages[1].Contents[0])
	}
	if secondText.Text != "Hello, Ada!" {
		t.Fatalf("second text = %q, want Hello, Ada!", secondText.Text)
	}
}

func callSingleMCPContent(t *testing.T, content mcp.Content) message.Content {
	t.Helper()
	result := callMCPResult(t, &mcp.CallToolResult{Content: []mcp.Content{content}})
	contents, ok := result.([]message.Content)
	if !ok {
		t.Fatalf("Call() result is %T, want []message.Content", result)
	}
	if len(contents) != 1 {
		t.Fatalf("expected one content item, got %d", len(contents))
	}
	return contents[0]
}

func callMCPResult(t *testing.T, callResult *mcp.CallToolResult) any {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "content",
		Description: "returns one MCP content block",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return callResult, nil
	})

	session := connectInMemory(t, ctx, server)
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	funcTool := tools[0].(tool.FuncTool)
	result, err := funcTool.Call(ctx, `{}`)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	return result
}

func callAddedTool(t *testing.T, stub stubFuncTool) *mcp.CallToolResult {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	mcptool.AddTool(server, stub)
	session := connectInMemory(t, ctx, server)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: stub.name, Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	return result
}

func mustDecodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("DecodeString(%q) error = %v", value, err)
	}
	return data
}

func mustMap(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value is %T, want map[string]any", value)
	}
	return result
}

func sameNumber(value any, want float64) bool {
	switch value := value.(type) {
	case int:
		return float64(value) == want
	case int64:
		return float64(value) == want
	case float64:
		return value == want
	default:
		return false
	}
}

func connectInMemory(t *testing.T, ctx context.Context, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := mcptool.Connect(ctx, clientTransport)
	if err != nil {
		t.Fatalf("client Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

// A tool exposed via AddTool may return a typed-nil message.Content (e.g. a
// (*message.ErrorContent)(nil)). Converting that to an MCP result must not
// panic the server handler; it degrades to a "null" text result.
func TestAddToolTypedNilContentDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	mcptool.AddTool(server, stubFuncTool{
		name:        "typednil",
		description: "returns a typed-nil content",
		schema:      map[string]any{"type": "object"},
		call: func(context.Context, string) (any, error) {
			var ec *message.ErrorContent
			return ec, nil // typed-nil message.Content
		},
	})

	session := connectInMemory(t, ctx, server)
	tools, err := mcptool.ListTools(ctx, session)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	funcTool, ok := tools[0].(tool.FuncTool)
	if !ok {
		t.Fatalf("listed tool is %T, want tool.FuncTool", tools[0])
	}

	result, err := funcTool.Call(ctx, `{}`) // must not panic or error
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	contents, ok := result.([]message.Content)
	if !ok || len(contents) != 1 {
		t.Fatalf("Call() result = %#v, want one content item", result)
	}
	text, ok := contents[0].(*message.TextContent)
	if !ok || !strings.Contains(text.Text, "null") {
		t.Fatalf("content = %#v, want a TextContent containing \"null\"", contents[0])
	}
}
