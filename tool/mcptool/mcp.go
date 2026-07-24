// Copyright (c) Microsoft. All rights reserved.

// Package mcp provides integration with the Model Context Protocol (MCP).
// It allows agents to connect to external MCP servers via stdio, HTTP, or WebSocket
// and expose their tools and prompts as agent.Tool instances.
package mcptool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func AddTool(src *mcp.Server, tl tool.FuncTool) {
	src.AddTool(&mcp.Tool{
		Name:         tl.Name(),
		Description:  tl.Description(),
		InputSchema:  tl.Schema(),
		OutputSchema: objectSchemaOrNil(tl.ReturnSchema()),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := tl.Call(ctx, string(req.Params.Arguments))
		if err != nil {
			callResult := &mcp.CallToolResult{}
			callResult.SetError(err)
			return callResult, nil
		}
		return agentResultToMCPCallToolResult(result), nil
	})
}

func Connect(ctx context.Context, transport mcp.Transport) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "agent-framework-go-mcp-client",
		Version: "1.0.0",
	}, nil)
	return client.Connect(ctx, transport, nil)
}

func ListTools(ctx context.Context, session *mcp.ClientSession) ([]tool.Tool, error) {
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Create agent.Tool instances for each MCP tool
	result := make([]tool.Tool, 0, len(toolsResult.Tools))
	for _, mcpTool := range toolsResult.Tools {
		agentTool := newMCPToolWrapper(session, mcpTool)
		result = append(result, agentTool)
	}

	return result, nil
}

// ListPrompts returns the prompt descriptors currently offered by the MCP
// server. Unlike tools, MCP prompts are not exposed as agent.Tool instances:
// they materialize into conversation messages (via GetPrompt) rather than being
// invoked as tools, so callers receive the raw *mcp.Prompt descriptors.
func ListPrompts(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Prompt, error) {
	promptsResult, err := session.ListPrompts(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list prompts: %w", err)
	}
	return promptsResult.Prompts, nil
}

// GetPrompt retrieves the named prompt from the MCP server, applying the given
// template arguments, and converts the returned prompt messages into agent
// messages. Each MCP PromptMessage.Content is mapped with the same
// content-conversion used for tool results, so text, image, audio, and resource
// contents are preserved.
func GetPrompt(ctx context.Context, session *mcp.ClientSession, name string, args map[string]string) ([]*message.Message, error) {
	promptResult, err := session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get prompt %q: %w", name, err)
	}

	messages := make([]*message.Message, 0, len(promptResult.Messages))
	for _, promptMessage := range promptResult.Messages {
		if promptMessage == nil {
			continue
		}
		contents := mcpContentToAgentContent([]mcp.Content{promptMessage.Content})
		msg := message.New(contents...)
		msg.Role = mcpRoleToAgentRole(promptMessage.Role)
		msg.RawRepresentation = promptMessage
		messages = append(messages, msg)
	}
	return messages, nil
}

// mcpRoleToAgentRole maps an MCP conversation role onto the agent-framework
// role. MCP only defines "user" and "assistant"; anything else defaults to the
// user role, matching how a prompt materializes as user-provided context.
func mcpRoleToAgentRole(role mcp.Role) message.Role {
	switch role {
	case "assistant":
		return message.RoleAssistant
	default:
		return message.RoleUser
	}
}

func mcpCallToolResultToAgentContent(result *mcp.CallToolResult) []message.Content {
	if result == nil {
		return nil
	}

	if mcpCallToolResultNeedsEnvelope(result) {
		return []message.Content{
			&message.TextContent{
				ContentHeader: mcpContentHeader(result),
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
	return result.IsError || result.StructuredContent != nil || len(result.Meta) > 0
}

func mcpContentToAgentContent(mcpContents []mcp.Content) []message.Content {
	return mcpContentToAgentContentWithRaw(mcpContents, nil)
}

func mcpContentToAgentContentWithRaw(mcpContents []mcp.Content, rawOverride any) []message.Content {
	if len(mcpContents) == 0 {
		return nil
	}

	result := make([]message.Content, 0, len(mcpContents))

	for _, contentValue := range mcpContents {
		var raw any = contentValue
		if rawOverride != nil {
			raw = rawOverride
		}

		switch contentValue := contentValue.(type) {
		case *mcp.TextContent:
			result = append(result, &message.TextContent{
				ContentHeader: mcpContentHeader(raw),
				Text:          contentValue.Text,
			})

		case *mcp.ImageContent:
			data, mediaType := mcpDataContent(contentValue.Data, contentValue.MIMEType, "image/*")
			result = append(result, &message.DataContent{
				ContentHeader: mcpContentHeader(raw),
				Data:          data,
				MediaType:     mediaType,
			})

		case *mcp.AudioContent:
			data, mediaType := mcpDataContent(contentValue.Data, contentValue.MIMEType, "audio/*")
			result = append(result, &message.DataContent{
				ContentHeader: mcpContentHeader(raw),
				Data:          data,
				MediaType:     mediaType,
			})

		case *mcp.ResourceLink:
			result = append(result, &message.URIContent{
				ContentHeader: mcpContentHeader(raw),
				MediaType:     contentValue.MIMEType,
				URI:           contentValue.URI,
			})

		case *mcp.EmbeddedResource:
			if contentValue.Resource == nil {
				result = append(result, &message.TextContent{
					ContentHeader: mcpContentHeader(raw),
					Text:          "[MCP embedded resource missing resource data]",
				})
				continue
			}
			header := mcpContentHeader(raw)
			if contentValue.Resource.Text != "" {
				result = append(result, &message.TextContent{
					ContentHeader: header,
					Text:          contentValue.Resource.Text,
				})
			} else {
				data, mediaType := mcpDataContent(contentValue.Resource.Blob, contentValue.Resource.MIMEType, "application/octet-stream")
				result = append(result, &message.DataContent{
					ContentHeader: header,
					Data:          data,
					MediaType:     mediaType,
					Name:          contentValue.Resource.URI,
				})
			}

		case *mcp.ToolUseContent:
			result = append(result, &message.TextContent{
				ContentHeader: mcpContentHeader(raw),
				Text:          jsonText(contentValue),
			})

		case *mcp.ToolResultContent:
			nestedContents := mcpContentToAgentContentWithRaw(contentValue.Content, contentValue)
			if len(nestedContents) > 0 {
				result = append(result, nestedContents...)
			} else {
				result = append(result, &message.TextContent{
					ContentHeader: mcpContentHeader(raw),
					Text:          jsonText(contentValue.StructuredContent),
				})
			}

		default:
			result = append(result, &message.TextContent{
				ContentHeader: mcpContentHeader(raw),
				Text:          fmt.Sprintf("[Unknown MCP content type: %T]", contentValue),
			})
		}
	}

	return result
}

func mcpContentHeader(raw any) message.ContentHeader {
	return message.ContentHeader{
		RawRepresentation: raw,
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

func objectSchemaOrNil(schema any) any {
	if schema == nil {
		return nil
	}
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		data, err := json.Marshal(schema)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(data, &schemaMap); err != nil {
			return nil
		}
	}
	if schemaMap["type"] != "object" {
		return nil
	}
	return schema
}

func agentResultToMCPCallToolResult(result any) *mcp.CallToolResult {
	switch resultValue := result.(type) {
	case nil:
		return &mcp.CallToolResult{}
	case *mcp.CallToolResult:
		return resultValue
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
			return &mcp.TextContent{Text: c.Text}
		}
	case *message.ErrorContent:
		if c != nil {
			return &mcp.TextContent{Text: c.Message}
		}
	case *message.DataContent:
		if c != nil {
			data, err := base64.StdEncoding.DecodeString(c.Data)
			if err != nil {
				return &mcp.TextContent{Text: fmt.Sprintf("[Invalid data content: %v]", err)}
			}
			switch c.TopLevelMediaType() {
			case "image":
				return &mcp.ImageContent{Data: data, MIMEType: c.MediaType}
			case "audio":
				return &mcp.AudioContent{Data: data, MIMEType: c.MediaType}
			case "text":
				// Text resources carry their payload in Text, not Blob. The reverse
				// mapping (mcpContentToAgentContent) already reads Resource.Text for
				// text; emitting Blob here would make text unreadable to MCP clients.
				// Non-UTF-8 payloads cannot survive JSON transport as Text (invalid
				// sequences are replaced), so fall back to Blob for those.
				if utf8.Valid(data) {
					return &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
						URI:      c.Name,
						MIMEType: c.MediaType,
						Text:     string(data),
					}}
				}
				return &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
					URI:      c.Name,
					MIMEType: c.MediaType,
					Blob:     data,
				}}
			default:
				return &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
					URI:      c.Name,
					MIMEType: c.MediaType,
					Blob:     data,
				}}
			}
		}
	case *message.URIContent:
		if c != nil {
			return &mcp.ResourceLink{URI: c.URI, MIMEType: c.MediaType}
		}
	}
	return &mcp.TextContent{Text: jsonText(contentValue)}
}

var (
	_ tool.Tool     = (*mcpWrapper)(nil)
	_ tool.FuncTool = (*mcpWrapper)(nil)
)

// mcpWrapper wraps an MCP tool as an agent.Tool.
type mcpWrapper struct {
	session *mcp.ClientSession
	tool    *mcp.Tool
}

func newMCPToolWrapper(session *mcp.ClientSession, tool *mcp.Tool) *mcpWrapper {
	return &mcpWrapper{
		session: session,
		tool:    tool,
	}
}

func (w *mcpWrapper) Name() string {
	return w.tool.Name
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
