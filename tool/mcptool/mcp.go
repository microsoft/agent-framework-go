// Copyright (c) Microsoft. All rights reserved.

// Package mcptool provides integration with the Model Context Protocol (MCP).
// It allows agents to connect to external MCP servers via stdio (subprocess)
// or HTTP (SSE / streamable HTTP) and expose their tools as
// tool.Tool / tool.FuncTool instances.
package mcptool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddTool registers a tool.FuncTool on the given mcp.Server so it is exposed to MCP clients.
// Native mcp.Content and []mcp.Content results are returned as MCP content blocks;
// they must contain content types valid in a tool response. Nil content entries
// are represented as the text "null". A *mcp.CallToolResult is returned as-is.
// Array return schemas and their structured results are wrapped in an object with
// a "result" property so structured output also works with older MCP clients.
// The text content retains the original JSON for existing clients.
// Explicit MCP results and MCP/framework content bypass this wrapping. When an
// output schema is advertised, callers returning these types must provide
// structured content matching that schema for successful results.
func AddTool(src *mcp.Server, tl tool.FuncTool) {
	outputSchema, wrapOutput := mcpOutputSchema(tl.ReturnSchema())
	src.AddTool(&mcp.Tool{
		Name:         tl.Name(),
		Description:  tl.Description(),
		InputSchema:  tl.Schema(),
		OutputSchema: outputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := tl.Call(ctx, string(req.Params.Arguments))
		if err != nil {
			callResult := &mcp.CallToolResult{}
			callResult.SetError(err)
			return callResult, nil
		}
		if wrapOutput {
			switch result.(type) {
			case *mcp.CallToolResult, mcp.Content, []mcp.Content, message.Content, message.Contents, []message.Content:
				// Explicit content results retain their existing conversion behavior.
			default:
				data, err := json.Marshal(result)
				if err != nil {
					callResult := &mcp.CallToolResult{}
					callResult.SetError(fmt.Errorf("marshaling array tool result: %w", err))
					return callResult, nil
				}
				text := string(data)
				if raw, ok := result.(json.RawMessage); ok {
					text = string(raw)
				}
				return &mcp.CallToolResult{
					Content:           []mcp.Content{&mcp.TextContent{Text: text}},
					StructuredContent: map[string]any{"result": json.RawMessage(data)},
				}, nil
			}
		}
		return agentResultToMCPCallToolResult(result), nil
	})
}

// Connect dials an MCP server over the given transport and returns a client session.
func Connect(ctx context.Context, transport mcp.Transport) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "agent-framework-go-mcp-client",
		Version: "1.0.0",
	}, nil)
	return client.Connect(ctx, transport, nil)
}

// ListTools enumerates all pages of the remote server's tools and wraps each as a tool.Tool.
func ListTools(ctx context.Context, session *mcp.ClientSession) ([]tool.Tool, error) {
	// Create agent.Tool instances for each MCP tool.
	//
	// Normalization (normalizeMCPName) can map distinct remote names onto the
	// same provider-safe name (e.g. "a b" and "a/b" both become "a-b"). Such a
	// collision would break provider tool registration (duplicate function
	// names) and cause the autocall tools map to silently drop all but the
	// first tool. Detect it here and fail loudly so the caller gets a clear
	// signal instead of missing/unreachable tools.
	// Create tool.Tool instances for each MCP tool
	result := make([]tool.Tool, 0)
	seen := make(map[string]string)
	for mcpTool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("failed to list tools: %w", err)
		}
		agentTool := newMCPToolWrapper(session, mcpTool)
		if existing, ok := seen[agentTool.name]; ok {
			return nil, fmt.Errorf("normalized MCP tool name collision: remote tools %q and %q both normalize to %q", existing, mcpTool.Name, agentTool.name)
		}
		seen[agentTool.name] = mcpTool.Name
		result = append(result, agentTool)
	}

	return result, nil
}

func mcpCallToolResultToAgentContent(result *mcp.CallToolResult) message.Contents {
	if result == nil {
		return nil
	}

	if mcpCallToolResultNeedsEnvelope(result) {
		return message.Contents{
			&message.TextContent{
				ContentHeader: mcpContentHeader(result, nil),
				Text:          jsonText(result),
			},
		}
	}

	contents := mcpContentToAgentContent(result.Content)
	if len(contents) > 0 {
		return contents
	}

	return nil
}

func mcpCallToolResultNeedsEnvelope(result *mcp.CallToolResult) bool {
	return result.IsError || result.StructuredContent != nil || hasUserDefinedMeta(result.Meta)
}

// hasUserDefinedMeta reports whether the meta map contains any keys that are
// not automatically injected by the MCP SDK (which prefixes its own keys with
// "io.modelcontextprotocol/"). SDK-injected keys such as
// MetaKeyServerInfo (added per SEP-2575 in v1.7.0) are not considered
// user-defined and do not warrant wrapping the result in an envelope.
func hasUserDefinedMeta(meta mcp.Meta) bool {
	for k := range meta {
		if !strings.HasPrefix(k, "io.modelcontextprotocol/") {
			return true
		}
	}
	return false
}

func mcpContentToAgentContent(mcpContents []mcp.Content) message.Contents {
	return mcpContentToAgentContentWithRaw(mcpContents, nil)
}

func mcpContentToAgentContentWithRaw(mcpContents []mcp.Content, rawOverride any) message.Contents {
	if len(mcpContents) == 0 {
		return nil
	}

	result := make(message.Contents, 0, len(mcpContents))

	for _, contentValue := range mcpContents {
		var raw any = contentValue
		if rawOverride != nil {
			raw = rawOverride
		}

		switch contentValue := contentValue.(type) {
		case *mcp.TextContent:
			result = append(result, &message.TextContent{
				ContentHeader: mcpContentHeader(raw, contentValue.Meta),
				Text:          contentValue.Text,
			})

		case *mcp.ImageContent:
			data, mediaType := mcpDataContent(contentValue.Data, contentValue.MIMEType, "image/*")
			result = append(result, &message.DataContent{
				ContentHeader: mcpContentHeader(raw, contentValue.Meta),
				Data:          data,
				MediaType:     mediaType,
			})

		case *mcp.AudioContent:
			data, mediaType := mcpDataContent(contentValue.Data, contentValue.MIMEType, "audio/*")
			result = append(result, &message.DataContent{
				ContentHeader: mcpContentHeader(raw, contentValue.Meta),
				Data:          data,
				MediaType:     mediaType,
			})

		case *mcp.ResourceLink:
			result = append(result, &message.URIContent{
				ContentHeader: mcpContentHeader(raw, contentValue.Meta),
				MediaType:     contentValue.MIMEType,
				URI:           contentValue.URI,
			})

		case *mcp.EmbeddedResource:
			result = append(result, mcpEmbeddedResourceToAgentContent(contentValue, raw))

		case *mcp.ToolUseContent: //nolint:staticcheck // ToolUseContent is deprecated per SEP-2577 but remains functional during the deprecation window.
			result = append(result, &message.TextContent{
				ContentHeader: mcpContentHeader(raw, contentValue.Meta),
				Text:          jsonText(contentValue),
			})

		case *mcp.ToolResultContent: //nolint:staticcheck // ToolResultContent is deprecated per SEP-2577 but remains functional during the deprecation window.
			nestedContents := mcpContentToAgentContentWithRaw(contentValue.Content, contentValue)
			if len(nestedContents) > 0 {
				result = append(result, nestedContents...)
			} else {
				result = append(result, &message.TextContent{
					ContentHeader: mcpContentHeader(raw, contentValue.Meta),
					Text:          jsonText(contentValue.StructuredContent),
				})
			}

		default:
			result = append(result, &message.TextContent{
				ContentHeader: mcpContentHeader(raw, nil),
				Text:          fmt.Sprintf("[Unknown MCP content type: %T]", contentValue),
			})
		}
	}

	return result
}

func mcpEmbeddedResourceToAgentContent(contentValue *mcp.EmbeddedResource, raw any) message.Content {
	if contentValue.Resource == nil {
		return &message.TextContent{
			ContentHeader: mcpContentHeader(raw, contentValue.Meta),
			Text:          "[MCP embedded resource missing resource data]",
		}
	}

	header := mcpContentHeader(raw, contentValue.Meta)
	if contentValue.Resource.Text != "" {
		return &message.TextContent{
			ContentHeader: header,
			Text:          contentValue.Resource.Text,
		}
	}

	data, mediaType := mcpDataContent(contentValue.Resource.Blob, contentValue.Resource.MIMEType, "application/octet-stream")
	return &message.DataContent{
		ContentHeader: header,
		Data:          data,
		MediaType:     mediaType,
		Name:          contentValue.Resource.URI,
	}
}

func mcpContentHeader(raw any, meta mcp.Meta) message.ContentHeader {
	return message.ContentHeader{
		RawRepresentation:    raw,
		AdditionalProperties: maps.Clone(meta),
	}
}

func mcpDataContent(data []byte, mediaType string, defaultMediaType string) (string, string) {
	if payload, dataURIMediaType, ok := dataURIBase64Payload(string(data)); ok {
		if mediaType == "" {
			mediaType = dataURIMediaType
		}
		if mediaType == "" {
			mediaType = defaultMediaType
		}
		return payload, mediaType
	}
	if mediaType == "" {
		mediaType = defaultMediaType
	}
	return base64.StdEncoding.EncodeToString(data), mediaType
}

func dataURIBase64Payload(value string) (string, string, bool) {
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return "", "", false
	}
	commaIndex := strings.IndexByte(value, ',')
	if commaIndex < 0 {
		return "", "", false
	}
	metadata := value[len("data:"):commaIndex]
	if !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		return "", "", false
	}
	return value[commaIndex+1:], metadata[:len(metadata)-len(";base64")], true
}

func jsonText(value any) string {
	if value == nil {
		return "null"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func mcpOutputSchema(schema any) (any, bool) {
	if schema == nil {
		return nil, false
	}
	// Decode a copy so relocating references never changes the tool's schema.
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	var schemaMap map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&schemaMap); err != nil {
		return nil, false
	}
	if schemaMap["type"] == "object" {
		return schema, false
	}
	isArray := schemaMap["type"] == "array"
	if types, ok := schemaMap["type"].([]any); ok {
		for _, typ := range types {
			if typ != "array" && typ != "null" {
				return nil, false
			}
			if typ == "array" {
				isArray = true
			}
		}
	}
	if !isArray {
		return nil, false
	}
	relocateOutputSchemaRefs(schemaMap)
	wrapped := map[string]any{
		"type":       "object",
		"properties": map[string]any{"result": schemaMap},
		"required":   []string{"result"},
	}
	if dialect, ok := schemaMap["$schema"]; ok {
		wrapped["$schema"] = dialect
	}
	return wrapped, true
}

// The schema moves under properties.result. Only visit schema keywords, leaving
// JSON data in defaults/examples unchanged. A nested $id establishes its own root.
func relocateOutputSchemaRefs(value any) {
	switch schema := value.(type) {
	case map[string]any:
		if id, _ := schema["$id"].(string); id != "" {
			return
		}
		for _, key := range []string{"$ref", "$dynamicRef"} {
			ref, _ := schema[key].(string)
			if !strings.HasPrefix(ref, "#") {
				continue
			}
			fragment, err := url.PathUnescape(ref[1:])
			if err == nil && (fragment == "" || strings.HasPrefix(fragment, "/")) {
				schema[key] = "#/properties/result" + ref[1:]
			}
		}
		for key, child := range schema {
			switch key {
			case "$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies":
				if children, ok := child.(map[string]any); ok {
					for _, child := range children {
						relocateOutputSchemaRefs(child)
					}
				}
			case "items", "prefixItems", "contains", "contentSchema", "additionalItems", "additionalProperties",
				"unevaluatedItems", "unevaluatedProperties", "propertyNames", "allOf", "anyOf", "oneOf", "not", "if", "then", "else":
				relocateOutputSchemaRefs(child)
			}
		}
	case []any:
		for _, child := range schema {
			relocateOutputSchemaRefs(child)
		}
	}
}

func agentResultToMCPCallToolResult(result any) *mcp.CallToolResult {
	switch resultValue := result.(type) {
	case nil:
		return &mcp.CallToolResult{}
	case *mcp.CallToolResult:
		return resultValue
	case mcp.Content:
		return &mcp.CallToolResult{Content: []mcp.Content{nativeMCPContentOrNull(resultValue)}}
	case []mcp.Content:
		contents := make([]mcp.Content, len(resultValue))
		for i, content := range resultValue {
			contents[i] = nativeMCPContentOrNull(content)
		}
		return &mcp.CallToolResult{Content: contents}
	case string:
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: resultValue}}}
	case json.RawMessage:
		return jsonResultToMCPCallToolResult(resultValue)
	case message.Content:
		callResult := &mcp.CallToolResult{}
		if functionResult, ok := resultValue.(*message.FunctionResultContent); ok {
			return functionResultToMCPCallToolResult(functionResult)
		}
		if _, ok := resultValue.(*message.ErrorContent); ok {
			callResult.IsError = true
		}
		callResult.Content = []mcp.Content{agentContentToMCPContent(resultValue)}
		return callResult
	case message.Contents:
		return agentResultToMCPCallToolResult([]message.Content(resultValue))
	case []message.Content:
		callResult := &mcp.CallToolResult{Content: make([]mcp.Content, 0, len(resultValue))}
		for _, contentValue := range resultValue {
			if _, ok := contentValue.(*message.ErrorContent); ok {
				callResult.IsError = true
			}
			callResult.Content = append(callResult.Content, agentContentToMCPContent(contentValue))
		}
		return callResult
	default:
		return structuredResultToMCPCallToolResult(resultValue)
	}
}

// Native MCP content should remain a protocol content block. Nil pointers need
// a fallback because the SDK content marshalers dereference their receivers.
func nativeMCPContentOrNull(content mcp.Content) mcp.Content {
	if content == nil || (reflect.ValueOf(content).Kind() == reflect.Pointer && reflect.ValueOf(content).IsNil()) {
		return &mcp.TextContent{Text: "null"}
	}
	return content
}

func functionResultToMCPCallToolResult(functionResult *message.FunctionResultContent) *mcp.CallToolResult {
	callResult := agentResultToMCPCallToolResult(functionResult.Result)
	if functionResult.Error != nil {
		callResult.IsError = true
		if len(callResult.Content) == 0 {
			callResult.SetError(functionResult.Error)
		}
	}
	return callResult
}

func jsonResultToMCPCallToolResult(data json.RawMessage) *mcp.CallToolResult {
	callResult := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}
	if isJSONObject(data) {
		callResult.StructuredContent = data
	}
	return callResult
}

func structuredResultToMCPCallToolResult(result any) *mcp.CallToolResult {
	data, err := json.Marshal(result)
	if err != nil {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%v", result)}}}
	}
	callResult := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
	if isJSONObject(data) {
		callResult.StructuredContent = json.RawMessage(data)
	}
	return callResult
}

func isJSONObject(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	return strings.HasPrefix(trimmed, "{")
}

func agentContentToMCPContent(contentValue message.Content) mcp.Content {
	// Each case returns only for a non-nil concrete value. A typed-nil pointer
	// (e.g. a tool returning (*message.ErrorContent)(nil)) still satisfies the
	// message.Content interface, so it would otherwise reach a field
	// dereference below and panic; instead it falls through to the JSON
	// fallback, where a typed-nil pointer marshals to "null".
	switch c := contentValue.(type) {
	case *message.TextContent:
		if c != nil {
			return &mcp.TextContent{Text: c.Text, Meta: maps.Clone(c.AdditionalProperties)}
		}
	case *message.ErrorContent:
		if c != nil {
			return &mcp.TextContent{Text: c.Message, Meta: maps.Clone(c.AdditionalProperties)}
		}
	case *message.DataContent:
		if c != nil {
			meta := maps.Clone(c.AdditionalProperties)
			data, err := base64.StdEncoding.DecodeString(c.Data)
			if err != nil {
				return &mcp.TextContent{Text: fmt.Sprintf("[Invalid data content: %v]", err), Meta: meta}
			}
			switch c.TopLevelMediaType() {
			case "image":
				return &mcp.ImageContent{Data: data, MIMEType: c.MediaType, Meta: meta}
			case "audio":
				return &mcp.AudioContent{Data: data, MIMEType: c.MediaType, Meta: meta}
			case "text":
				// Text resources carry their payload in Text, not Blob. The reverse
				// mapping (mcpContentToAgentContent) already reads Resource.Text for
				// text; emitting Blob here would make text unreadable to MCP clients.
				// Non-UTF-8 payloads cannot survive JSON transport as Text (invalid
				// sequences are replaced), so fall back to Blob for those.
				if utf8.Valid(data) {
					return &mcp.EmbeddedResource{Meta: meta, Resource: &mcp.ResourceContents{
						URI:      c.Name,
						MIMEType: c.MediaType,
						Text:     string(data),
					}}
				}
				return &mcp.EmbeddedResource{Meta: meta, Resource: &mcp.ResourceContents{
					URI:      c.Name,
					MIMEType: c.MediaType,
					Blob:     data,
				}}
			default:
				return &mcp.EmbeddedResource{Meta: meta, Resource: &mcp.ResourceContents{
					URI:      c.Name,
					MIMEType: c.MediaType,
					Blob:     data,
				}}
			}
		}
	case *message.URIContent:
		if c != nil {
			// MCP requires a non-empty resource-link name; URIContent has none,
			// so default it to the URI (as the embedded-resource path already
			// does), rather than emitting an empty required field.
			return &mcp.ResourceLink{Name: c.URI, URI: c.URI, MIMEType: c.MediaType, Meta: maps.Clone(c.AdditionalProperties)}
		}
	}
	var meta mcp.Meta
	if contentValue != nil && (reflect.ValueOf(contentValue).Kind() != reflect.Pointer || !reflect.ValueOf(contentValue).IsNil()) {
		meta = maps.Clone(contentValue.Header().AdditionalProperties)
	}
	return &mcp.TextContent{Text: jsonText(contentValue), Meta: meta}
}

var (
	_ tool.Tool     = (*mcpWrapper)(nil)
	_ tool.FuncTool = (*mcpWrapper)(nil)
)

// mcpWrapper wraps an MCP tool as a tool.Tool.
type mcpWrapper struct {
	session *mcp.ClientSession
	tool    *mcp.Tool
	// name is the normalized tool name surfaced to providers and used as the
	// autocall map key. tool.Name retains the original remote name used when
	// invoking the MCP server.
	name string
}

func newMCPToolWrapper(session *mcp.ClientSession, tool *mcp.Tool) *mcpWrapper {
	return &mcpWrapper{
		session: session,
		tool:    tool,
		name:    normalizeMCPName(tool.Name),
	}
}

// normalizeMCPName replaces every rune that is not a valid function-name
// character with a dash. Providers such as OpenAI reject tool names that do not
// match the [A-Za-z0-9_.-] identifier pattern, but MCP server tool names may
// contain arbitrary characters (spaces, slashes, colons). This mirrors the
// Python SDK's _normalize_mcp_name so the same remote tool surfaces under the
// same name across SDKs.
func normalizeMCPName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
			return r
		default:
			return '-'
		}
	}, name)
}

func (w *mcpWrapper) Name() string {
	return w.name
}

func (w *mcpWrapper) Description() string {
	return w.tool.Description
}

func (w *mcpWrapper) Schema() any {
	return w.tool.InputSchema
}

func (w *mcpWrapper) ReturnSchema() any {
	return w.tool.OutputSchema
}

// Call implements the Func-like calling pattern for MCP tools.
func (w *mcpWrapper) Call(ctx context.Context, args string) (any, error) {
	result, err := w.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      w.tool.Name,
		Arguments: json.RawMessage(args),
	})
	if err != nil {
		return nil, fmt.Errorf("MCP tool call failed: %w", err)
	}

	contents := mcpCallToolResultToAgentContent(result)
	if contents == nil {
		return nil, nil
	}
	return contents, nil
}
