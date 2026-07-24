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

// ConnectOption configures the MCP client that Connect constructs. Options are
// additive: with no options Connect behaves exactly as before, advertising no
// sampling capability and ignoring server list-changed notifications.
type ConnectOption func(*mcp.ClientOptions)

// SamplingChatClient produces the host model's reply to a server-initiated
// sampling/createMessage request. It mirrors the Python client's
// sampling_callback and the .NET sampling IChatClient: an MCP server may
// delegate sub-reasoning to the host, which answers with the caller's model.
type SamplingChatClient interface {
	// GetResponse returns the assistant reply for the translated conversation.
	// maxTokens is the clamped token budget for the reply (0 when unbounded).
	GetResponse(ctx context.Context, messages []*message.Message, maxTokens int64) (*message.Message, error)
}

// SamplingApprover decides whether a server-initiated sampling request may run
// against the host model. Returning false, an error, or supplying a nil
// approver denies the request, mirroring the Python client's deny-by-default
// policy for host-in-the-loop sampling.
type SamplingApprover func(ctx context.Context, params *mcp.CreateMessageParams) (bool, error)

// WithSampling advertises the sampling capability and services incoming
// sampling/createMessage requests using client. Requests are denied unless
// approve is non-nil and returns true. maxTokens, when positive, clamps the
// server-requested token budget passed to the client.
func WithSampling(client SamplingChatClient, approve SamplingApprover, maxTokens int64) ConnectOption {
	return func(opts *mcp.ClientOptions) {
		opts.CreateMessageHandler = func(ctx context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			return handleSampling(ctx, client, approve, maxTokens, req.Params)
		}
	}
}

// WithToolListChanged invokes cb when the server sends
// notifications/tools/list_changed, letting the caller re-run ListTools to
// refresh a static tool set mid-session.
func WithToolListChanged(cb func()) ConnectOption {
	return func(opts *mcp.ClientOptions) {
		opts.ToolListChangedHandler = func(context.Context, *mcp.ToolListChangedRequest) {
			if cb != nil {
				cb()
			}
		}
	}
}

// WithPromptListChanged invokes cb when the server sends
// notifications/prompts/list_changed.
func WithPromptListChanged(cb func()) ConnectOption {
	return func(opts *mcp.ClientOptions) {
		opts.PromptListChangedHandler = func(context.Context, *mcp.PromptListChangedRequest) {
			if cb != nil {
				cb()
			}
		}
	}
}

func Connect(ctx context.Context, transport mcp.Transport, opts ...ConnectOption) (*mcp.ClientSession, error) {
	clientOptions := &mcp.ClientOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(clientOptions)
		}
	}
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "agent-framework-go-mcp-client",
		Version: "1.0.0",
	}, clientOptions)
	return client.Connect(ctx, transport, nil)
}

func handleSampling(ctx context.Context, client SamplingChatClient, approve SamplingApprover, maxTokens int64, params *mcp.CreateMessageParams) (*mcp.CreateMessageResult, error) {
	if client == nil {
		return nil, fmt.Errorf("mcp sampling: no chat client configured")
	}
	// Deny by default: a server may only borrow the host model when the caller
	// has explicitly opted in via an approval callback (matches Python).
	if approve == nil {
		return nil, fmt.Errorf("mcp sampling: request denied (no approval callback configured)")
	}
	approved, err := approve(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("mcp sampling: approval failed: %w", err)
	}
	if !approved {
		return nil, fmt.Errorf("mcp sampling: request denied by approval callback")
	}

	requested := int64(0)
	if params != nil {
		requested = params.MaxTokens
	}
	reply, err := client.GetResponse(ctx, samplingMessagesToAgent(params), clampMaxTokens(requested, maxTokens))
	if err != nil {
		return nil, fmt.Errorf("mcp sampling: chat client failed: %w", err)
	}

	return &mcp.CreateMessageResult{
		Role:       mcp.Role(message.RoleAssistant),
		Content:    samplingReplyContent(reply),
		StopReason: "endTurn",
	}, nil
}

// clampMaxTokens limits the server-requested token budget to limit. A
// non-positive limit leaves the request unbounded; a non-positive request
// adopts the limit.
func clampMaxTokens(requested, limit int64) int64 {
	if limit <= 0 {
		return requested
	}
	if requested <= 0 || requested > limit {
		return limit
	}
	return requested
}

func samplingMessagesToAgent(params *mcp.CreateMessageParams) []*message.Message {
	if params == nil {
		return nil
	}
	messages := make([]*message.Message, 0, len(params.Messages)+1)
	if params.SystemPrompt != "" {
		messages = append(messages, &message.Message{
			Role:     message.RoleSystem,
			Contents: message.Contents{&message.TextContent{Text: params.SystemPrompt}},
		})
	}
	for _, samplingMessage := range params.Messages {
		if samplingMessage == nil {
			continue
		}
		messages = append(messages, &message.Message{
			Role:     samplingRoleToAgent(samplingMessage.Role),
			Contents: mcpContentToAgentContent([]mcp.Content{samplingMessage.Content}),
		})
	}
	return messages
}

func samplingRoleToAgent(role mcp.Role) message.Role {
	if role == mcp.Role(message.RoleAssistant) {
		return message.RoleAssistant
	}
	return message.RoleUser
}

func samplingReplyContent(reply *message.Message) mcp.Content {
	if reply != nil {
		for _, content := range reply.Contents {
			if content != nil {
				return agentContentToMCPContent(content)
			}
		}
	}
	return &mcp.TextContent{}
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
