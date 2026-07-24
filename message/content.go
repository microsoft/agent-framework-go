// Copyright (c) Microsoft. All rights reserved.

package message

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/internal/jsonx"
)

var supportedContents map[contentKind]reflect.Type

func init() {
	supportedContents = make(map[contentKind]reflect.Type)
	for _, c := range []Content{
		&TextContent{},
		&DataContent{},
		&ErrorContent{},
		&FunctionCallContent{},
		&FunctionResultContent{},
		&HostedFileContent{},
		&HostedVectorStoreContent{},
		&TextReasoningContent{},
		&URIContent{},
		&UsageContent{},
		&ToolApprovalRequestContent{},
		&ToolApprovalResponseContent{},
		&AlwaysApproveToolApprovalResponseContent{},
		&MCPServerToolCallContent{},
		&CodeInterpreterToolCallContent{},
		&CodeInterpreterToolResultContent{},
	} {
		supportedContents[c.kind()] = reflect.TypeOf(c).Elem()
	}
}

// contentKind represents the type of content.
// It is unexported to prevent external implementations of Content.
type contentKind string

// ContentHeader contains common properties for all content types.
type ContentHeader struct {
	AdditionalProperties map[string]any `json:"-"`
	Annotations          Annotations    `json:",omitempty"`
	RawRepresentation    any            `json:"-"`
}

func (ch ContentHeader) Header() ContentHeader {
	return ch
}

// Content represents message content.
type Content interface {
	json.Marshaler
	kind() contentKind
	Header() ContentHeader
}

// ToolCallContent represents content that requests a tool call.
type ToolCallContent interface {
	Content

	GetCallID() string
}

// Contents is a slice of Content that supports JSON encoding.
type Contents []Content

func (cs *Contents) UnmarshalJSON(data []byte) error {
	out, err := jsonx.UnmarshalDiscriminatedUnionSliceWithFallback(data, supportedContents, unmarshalRawContent)
	if err != nil {
		return err
	}
	*cs = out
	return nil
}

func unmarshalRawContent(data json.RawMessage) (Content, error) {
	var raw RawContent
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

// Text returns the first text content in the response, or empty string.
func (cs Contents) Text() string {
	var sb strings.Builder
	for _, c := range cs {
		if textContent, ok := c.(*TextContent); ok {
			sb.WriteString(textContent.Text)
		}
	}
	return sb.String()
}

func (cs Contents) Usage() UsageDetails {
	var usage UsageDetails
	for _, c := range cs {
		if usageContent, ok := c.(*UsageContent); ok {
			usage.Add(usageContent.Details)
		}
	}
	return usage
}

// TextContent represents plain text content.
type TextContent struct {
	ContentHeader

	Text string
}

func (t *TextContent) MarshalJSON() ([]byte, error) {
	type alias TextContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t TextContent) kind() contentKind { return "text" }

// String returns the text of the content.
func (t *TextContent) String() string { return t.Text }

// DataContent represents binary content with an associated media type.
//
// The content represents in-memory data. For references to data at a remote URI,
// use [URIContent] instead.
type DataContent struct {
	ContentHeader

	// Name is an optional name associated with the data.
	// A service might use this name as part of citations or to help infer the type of data
	// being represented based on a file extension.
	Name string

	Data      string // base64-encoded data
	MediaType string
}

type serializedDataContent struct {
	ContentHeader

	Name string `json:",omitempty"`
	URI  string
	Type contentKind
}

func (t *DataContent) MarshalJSON() ([]byte, error) {
	tmp := serializedDataContent{
		ContentHeader: t.ContentHeader,
		Name:          t.Name,
		URI:           t.URI(),
		Type:          t.kind(),
	}
	return json.Marshal(tmp)
}

func (t *DataContent) UnmarshalJSON(data []byte) error {
	var tmp serializedDataContent
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	d, err := newDataContentFromURI(tmp.URI, "")
	if err != nil {
		return err
	}
	t.ContentHeader = tmp.ContentHeader
	t.Name = tmp.Name
	t.Data = d.Data
	t.MediaType = d.MediaType
	return nil
}

func (t DataContent) kind() contentKind { return "data" }

// URI returns the Data URI representation of the content,
// as defined in RFC 2397:
//
//	data:[<mediatype>][;base64],<data>
func (t *DataContent) URI() string {
	return fmt.Sprintf("data:%s;base64,%s", t.MediaType, t.Data)
}

// newDataContentFromURI creates a new DataContent from a Data URI string.
// The mediaType parameter is optional; if not provided, it must be present in the URI.
// Returns an error if the URI is invalid or doesn't contain a media type when required.
func newDataContentFromURI(uri string, mediaType string) (*DataContent, error) {
	if uri == "" {
		return nil, fmt.Errorf("uri cannot be empty")
	}

	// Validate that it's a data URI
	if !strings.HasPrefix(strings.ToLower(uri), dataURIScheme) {
		return nil, fmt.Errorf("the provided URI is not a data URI")
	}

	// Parse the data URI to extract the data and media type
	parsedURI, err := parseDataURI(uri)
	if err != nil {
		return nil, err
	}

	// Determine the media type to use
	mediaType = cmp.Or(mediaType, parsedURI.MediaType)
	if mediaType == "" {
		return nil, fmt.Errorf("uri did not contain a media type, and mediaType was not provided")
	}

	// Validate media type
	if !isValidMediaType(mediaType) {
		return nil, fmt.Errorf("invalid media type: %s", mediaType)
	}
	return &DataContent{
		Data:      parsedURI.data(),
		MediaType: mediaType,
	}, nil
}

// Bytes returns the decoded data.
func (t *DataContent) Bytes() ([]byte, error) {
	return base64.StdEncoding.DecodeString(t.Data)
}

func (t *DataContent) TopLevelMediaType() string {
	return topLevelMediaType(t.MediaType)
}

// ErrorContent represents an error.
//
// Typically, ErrorContent is used for non-fatal errors, where something went wrong as part
// of the operation but the operation was still able to continue.
type ErrorContent struct {
	ContentHeader

	Message   string
	Details   string `json:",omitempty"`
	ErrorCode string `json:",omitempty"`
}

func (t *ErrorContent) MarshalJSON() ([]byte, error) {
	type alias ErrorContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t ErrorContent) kind() contentKind { return "error" }

// MCPServerToolCallContent represents a tool call request to a MCP server.
//
// This content type is used to represent an invocation of an MCP server tool
// by a hosted service. It is informational only and may appear as part of an
// approval request to convey what is being approved, or as a record of which
// MCP server tool was invoked.
type MCPServerToolCallContent struct {
	ContentHeader

	Arguments  string
	CallID     string
	Name       string
	ServerName string `json:",omitempty"`
}

func (t MCPServerToolCallContent) kind() contentKind { return "mcpServerToolCall" }

func (t *MCPServerToolCallContent) GetCallID() string {
	if t == nil {
		return ""
	}
	return t.CallID
}

func (t *MCPServerToolCallContent) MarshalJSON() ([]byte, error) {
	type alias MCPServerToolCallContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

type serializedFunctionCallContent struct {
	ContentHeader

	Arguments         string
	CallID            string
	Error             string `json:",omitempty"`
	Name              string
	InformationalOnly bool

	Type contentKind
}

// FunctionCallContent represents a function call request.
type FunctionCallContent struct {
	ContentHeader

	Arguments         string
	CallID            string
	Error             error // Error that occurred while mapping the original function call data to this object.
	Name              string
	InformationalOnly bool
}

func (t *FunctionCallContent) MarshalJSON() ([]byte, error) {
	tmp := serializedFunctionCallContent{
		ContentHeader:     t.ContentHeader,
		Arguments:         t.Arguments,
		CallID:            t.CallID,
		Name:              t.Name,
		InformationalOnly: t.InformationalOnly,
		Type:              t.kind(),
	}
	if t.Error != nil {
		tmp.Error = t.Error.Error()
	}
	return json.Marshal(tmp)
}

func (t *FunctionCallContent) UnmarshalJSON(data []byte) error {
	var tmp serializedFunctionCallContent
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	t.ContentHeader = tmp.ContentHeader
	t.Arguments = tmp.Arguments
	t.CallID = tmp.CallID
	t.Name = tmp.Name
	t.InformationalOnly = tmp.InformationalOnly
	if tmp.Error != "" {
		t.Error = errors.New(tmp.Error)
	}
	return nil
}

func (t FunctionCallContent) kind() contentKind { return "functionCall" }

func (t *FunctionCallContent) GetCallID() string {
	if t == nil {
		return ""
	}
	return t.CallID
}

type serializedFunctionResultContent struct {
	ContentHeader

	CallID string
	Error  string `json:",omitempty"`
	Result any    `json:",omitempty"`

	Type contentKind
}

// FunctionResultContent represents the result of a function call.
type FunctionResultContent struct {
	ContentHeader

	CallID string
	Error  error `json:",omitempty"` // Error that occurred if the function call failed.
	Result any   `json:",omitempty"`
}

func (t *FunctionResultContent) MarshalJSON() ([]byte, error) {
	tmp := serializedFunctionResultContent{
		ContentHeader: t.ContentHeader,
		CallID:        t.CallID,
		Result:        t.Result,
		Type:          t.kind(),
	}
	if t.Error != nil {
		tmp.Error = t.Error.Error()
	}
	return json.Marshal(tmp)
}

func (t *FunctionResultContent) UnmarshalJSON(data []byte) error {
	var tmp serializedFunctionResultContent
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	t.ContentHeader = tmp.ContentHeader
	t.CallID = tmp.CallID
	t.Result = tmp.Result
	if tmp.Error != "" {
		t.Error = errors.New(tmp.Error)
	}
	return nil
}

func (t FunctionResultContent) kind() contentKind { return "functionResult" }

// HostedFileContent represents a file that is hosted by the AI service.
//
// Unlike [DataContent] which contains the data for a file or blob, this class represents a file
// that is hosted by the AI service and referenced by an identifier.
// Such identifiers are specific to the provider.
type HostedFileContent struct {
	ContentHeader

	FileID    string
	Name      string `json:",omitempty"`
	MediaType string `json:",omitempty"`
}

func (t *HostedFileContent) TopLevelMediaType() string {
	return topLevelMediaType(t.MediaType)
}

func (t *HostedFileContent) MarshalJSON() ([]byte, error) {
	type alias HostedFileContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t HostedFileContent) kind() contentKind { return "hostedFile" }

// HostedVectorStoreContent represents a vector store that is hosted by the AI service.
//
// Unlike [HostedFileContent] which represents a specific file that is hosted by the AI service,
// HostedVectorStoreContent represents a vector store that can contain multiple files, indexed for searching.
type HostedVectorStoreContent struct {
	ContentHeader

	VectorStoreID string
}

func (t *HostedVectorStoreContent) MarshalJSON() ([]byte, error) {
	type alias HostedVectorStoreContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t HostedVectorStoreContent) kind() contentKind { return "hostedVectorStore" }

// RawContent represents provider-specific content that does not fit one of the
// structured content types. The provider value is available through
// [ContentHeader.RawRepresentation].
type RawContent struct {
	ContentHeader
}

func (t *RawContent) MarshalJSON() ([]byte, error) {
	if raw, ok := t.RawRepresentation.(json.RawMessage); ok && len(raw) > 0 {
		return raw, nil
	}
	type alias RawContent
	return json.Marshal((*alias)(t))
}

func (t *RawContent) UnmarshalJSON(data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("invalid raw content JSON")
	}
	var header ContentHeader
	if err := json.Unmarshal(data, &header); err == nil {
		t.ContentHeader = header
	}
	t.RawRepresentation = append(json.RawMessage(nil), data...)
	return nil
}

func (t RawContent) kind() contentKind { return "" }

// TextReasoningContent represents text reasoning content in a chat.
//
// TextReasoningContent is distinct from [TextContent]. TextReasoningContent represents "thinking" or "reasoning"
// performed by the model and is distinct from the actual output text from the model,
// which is represented by [TextContent].
type TextReasoningContent struct {
	ContentHeader

	ProtectedData string `json:",omitempty"`
	Text          string
}

func (t *TextReasoningContent) MarshalJSON() ([]byte, error) {
	type alias TextReasoningContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t TextReasoningContent) kind() contentKind { return "reasoning" }

// String returns the text of the reasoning content.
func (t *TextReasoningContent) String() string { return t.Text }

// URIContent represents a URL, typically to hosted content such as an image, audio, or video.
type URIContent struct {
	ContentHeader

	MediaType string
	URI       string
}

func NewURIContent(uri string, mediaType string) (*URIContent, error) {
	if err := validateURIContentURI(uri); err != nil {
		return nil, err
	}
	if mediaType == "" {
		mediaType = inferMediaTypeFromURI(uri)
	} else if !isValidMediaType(mediaType) {
		return nil, fmt.Errorf("invalid media type: %s", mediaType)
	}
	return &URIContent{URI: uri, MediaType: mediaType}, nil
}

func (t *URIContent) TopLevelMediaType() string {
	return topLevelMediaType(t.MediaType)
}

func (t *URIContent) MarshalJSON() ([]byte, error) {
	type alias URIContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t URIContent) kind() contentKind { return "uri" }

// UsageDetails provides usage details about a request/response.
type UsageDetails struct {
	AdditionalCounts      map[string]int64
	InputTokenCount       int64
	OutputTokenCount      int64
	TotalTokenCount       int64
	CachedInputTokenCount int64
	ReasoningTokenCount   int64
}

func (u *UsageDetails) Add(other UsageDetails) {
	u.InputTokenCount += other.InputTokenCount
	u.OutputTokenCount += other.OutputTokenCount
	u.TotalTokenCount += other.TotalTokenCount
	u.CachedInputTokenCount += other.CachedInputTokenCount
	u.ReasoningTokenCount += other.ReasoningTokenCount

	// Merge additional counts
	if other.AdditionalCounts != nil {
		if u.AdditionalCounts == nil {
			u.AdditionalCounts = make(map[string]int64)
		}
		for k, v := range other.AdditionalCounts {
			u.AdditionalCounts[k] += v
		}
	}
}

// UsageContent represents usage information associated with a chat request and response.
type UsageContent struct {
	ContentHeader

	Details UsageDetails
}

func (t *UsageContent) MarshalJSON() ([]byte, error) {
	type alias UsageContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t UsageContent) kind() contentKind { return "usage" }

type serializedToolApprovalRequestContent struct {
	ContentHeader

	RequestID string
	ToolCall  ToolCallContent

	Type contentKind
}

type serializedToolApprovalRequestContentForUnmarshal struct {
	ContentHeader

	RequestID string
	ToolCall  json.RawMessage
}

// ToolApprovalRequestContent represents a request for approval to execute a tool.
type ToolApprovalRequestContent struct {
	ContentHeader

	RequestID string
	ToolCall  ToolCallContent
}

func (t *ToolApprovalRequestContent) MarshalJSON() ([]byte, error) {
	tmp := serializedToolApprovalRequestContent{
		ContentHeader: t.ContentHeader,
		RequestID:     t.RequestID,
		ToolCall:      t.ToolCall,
		Type:          t.kind(),
	}
	return json.Marshal(tmp)
}

func (t *ToolApprovalRequestContent) UnmarshalJSON(data []byte) error {
	var tmp serializedToolApprovalRequestContentForUnmarshal
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	toolCall, err := unmarshalToolApprovalToolCall(tmp.ToolCall)
	if err != nil {
		return err
	}
	t.ContentHeader = tmp.ContentHeader
	t.RequestID = tmp.RequestID
	t.ToolCall = toolCall
	return nil
}

func (t ToolApprovalRequestContent) kind() contentKind { return "toolApprovalRequest" }

func (t *ToolApprovalRequestContent) CreateResponse(approved bool, reason string) *ToolApprovalResponseContent {
	return &ToolApprovalResponseContent{
		RequestID: t.RequestID,
		Approved:  approved,
		Reason:    reason,
		ToolCall:  cloneToolCallContent(t.ToolCall),
		ContentHeader: ContentHeader{
			AdditionalProperties: maps.Clone(t.AdditionalProperties),
			Annotations:          slices.Clone(t.Annotations),
			RawRepresentation:    t.RawRepresentation,
		},
	}
}

func cloneToolCallContent(toolCall ToolCallContent) ToolCallContent {
	switch toolCall := toolCall.(type) {
	case nil:
		return nil
	case *FunctionCallContent:
		if toolCall == nil {
			return nil
		}
		cloned := *toolCall
		cloned.ContentHeader = cloneContentHeader(toolCall.ContentHeader)
		return &cloned
	case *MCPServerToolCallContent:
		if toolCall == nil {
			return nil
		}
		cloned := *toolCall
		cloned.ContentHeader = cloneContentHeader(toolCall.ContentHeader)
		return &cloned
	default:
		return toolCall
	}
}

func cloneContentHeader(header ContentHeader) ContentHeader {
	return ContentHeader{
		AdditionalProperties: maps.Clone(header.AdditionalProperties),
		Annotations:          slices.Clone(header.Annotations),
		RawRepresentation:    header.RawRepresentation,
	}
}

// AlwaysApproveToolResponse creates a response that approves this tool call and
// records a standing rule to automatically approve all future calls to the same
// tool, regardless of arguments.
func (t *ToolApprovalRequestContent) AlwaysApproveToolResponse() *AlwaysApproveToolApprovalResponseContent {
	return &AlwaysApproveToolApprovalResponseContent{
		InnerResponse:     t.CreateResponse(true, ""),
		AlwaysApproveTool: true,
		ContentHeader:     ContentHeader{AdditionalProperties: t.AdditionalProperties},
	}
}

// AlwaysApproveToolWithArgumentsResponse creates a response that approves this tool
// call and records a standing rule to automatically approve future calls to the
// same tool only when the arguments match exactly.
func (t *ToolApprovalRequestContent) AlwaysApproveToolWithArgumentsResponse() *AlwaysApproveToolApprovalResponseContent {
	return &AlwaysApproveToolApprovalResponseContent{
		InnerResponse:                  t.CreateResponse(true, ""),
		AlwaysApproveToolWithArguments: true,
		ContentHeader:                  ContentHeader{AdditionalProperties: t.AdditionalProperties},
	}
}

// Deprecated: Use AlwaysApproveToolWithArgumentsResponse instead.
// AlwaysApproveToolWithArgsResponse is an alias for AlwaysApproveToolWithArgumentsResponse.
func (t *ToolApprovalRequestContent) AlwaysApproveToolWithArgsResponse() *AlwaysApproveToolApprovalResponseContent {
	return t.AlwaysApproveToolWithArgumentsResponse()
}

type serializedToolApprovalResponseContent struct {
	ContentHeader

	RequestID string
	Reason    string `json:",omitempty"`
	Approved  bool
	ToolCall  ToolCallContent

	Type contentKind
}

type serializedToolApprovalResponseContentForUnmarshal struct {
	ContentHeader

	RequestID string
	Reason    string `json:",omitempty"`
	Approved  bool
	ToolCall  json.RawMessage
}

// ToolApprovalResponseContent represents a response to a [ToolApprovalRequestContent].
type ToolApprovalResponseContent struct {
	ContentHeader

	RequestID string
	Reason    string `json:",omitempty"`
	Approved  bool
	ToolCall  ToolCallContent
}

func (t *ToolApprovalResponseContent) MarshalJSON() ([]byte, error) {
	tmp := serializedToolApprovalResponseContent{
		ContentHeader: t.ContentHeader,
		RequestID:     t.RequestID,
		Reason:        t.Reason,
		Approved:      t.Approved,
		ToolCall:      t.ToolCall,
		Type:          t.kind(),
	}
	return json.Marshal(tmp)
}

func (t *ToolApprovalResponseContent) UnmarshalJSON(data []byte) error {
	var tmp serializedToolApprovalResponseContentForUnmarshal
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	toolCall, err := unmarshalToolApprovalToolCall(tmp.ToolCall)
	if err != nil {
		return err
	}
	t.ContentHeader = tmp.ContentHeader
	t.RequestID = tmp.RequestID
	t.Reason = tmp.Reason
	t.Approved = tmp.Approved
	t.ToolCall = toolCall
	return nil
}

func (t ToolApprovalResponseContent) kind() contentKind { return "toolApprovalResponse" }

func unmarshalToolApprovalToolCall(data json.RawMessage) (ToolCallContent, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var header struct {
		Type contentKind
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	switch header.Type {
	case contentKind("functionCall"):
		var toolCall FunctionCallContent
		if err := json.Unmarshal(data, &toolCall); err != nil {
			return nil, err
		}
		return &toolCall, nil
	case contentKind("mcpServerToolCall"):
		var toolCall MCPServerToolCallContent
		if err := json.Unmarshal(data, &toolCall); err != nil {
			return nil, err
		}
		return &toolCall, nil
	default:
		return nil, fmt.Errorf("unsupported tool call content type: %v", header.Type)
	}
}

// AlwaysApproveToolApprovalResponseContent wraps a tool approval response and
// adds standing rule hints for tool-approval middleware.
type AlwaysApproveToolApprovalResponseContent struct {
	ContentHeader

	InnerResponse *ToolApprovalResponseContent `json:"innerResponse"`

	// AlwaysApproveTool, when true, indicates a standing rule to auto-approve
	// all future calls to the same tool regardless of arguments.
	AlwaysApproveTool bool `json:"AlwaysApproveTool,omitempty"`

	// AlwaysApproveToolWithArguments, when true, indicates a standing rule to
	// auto-approve future calls to the same tool only when the arguments
	// match exactly.
	AlwaysApproveToolWithArguments bool `json:"AlwaysApproveToolWithArguments,omitempty"`
}

func (t *AlwaysApproveToolApprovalResponseContent) MarshalJSON() ([]byte, error) {
	type alias AlwaysApproveToolApprovalResponseContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t AlwaysApproveToolApprovalResponseContent) kind() contentKind {
	return "alwaysApproveToolApprovalResponse"
}

type CodeInterpreterToolCallContent struct {
	ContentHeader

	CallID string
	Inputs Contents
}

func (t *CodeInterpreterToolCallContent) MarshalJSON() ([]byte, error) {
	type alias CodeInterpreterToolCallContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t CodeInterpreterToolCallContent) kind() contentKind { return "codeInterpreterToolCall" }

type CodeInterpreterToolResultContent struct {
	ContentHeader

	CallID  string
	Outputs Contents
}

func (t *CodeInterpreterToolResultContent) MarshalJSON() ([]byte, error) {
	type alias CodeInterpreterToolResultContent
	tmp := struct {
		*alias
		Type contentKind
	}{
		alias: (*alias)(t),
		Type:  t.kind(),
	}
	return json.Marshal(tmp)
}

func (t CodeInterpreterToolResultContent) kind() contentKind { return "codeInterpreterToolResult" }

// CoalesceContents combines sequential contents elements.
func CoalesceContents(contents []Content) []Content {
	var sb strings.Builder
	mergeText := func(contents []Content, start, end int) string {
		sb.Reset()
		for _, c := range contents[start:end] {
			if tc, ok := c.(fmt.Stringer); ok {
				sb.WriteString(tc.String())
			}
		}
		return sb.String()
	}
	var buf bytes.Buffer
	mergeBase64 := func(contents []Content, start, end int) string {
		// Base64 strings can't be concatenated directly, so we need to decode and re-encode.
		defer buf.Reset()
		for _, c := range contents[start:end] {
			var bytes []byte
			switch c := c.(type) {
			case *DataContent:
				bytes, _ = c.Bytes() // if we got here it means the data URI is valid
			}
			if len(bytes) > 0 {
				buf.Write(bytes)
			}
		}
		return base64.StdEncoding.EncodeToString(buf.Bytes())
	}

	contents = coalesce(contents, false, nil, func(contents []Content, start, end int) *TextContent {
		return &TextContent{
			ContentHeader: ContentHeader{
				AdditionalProperties: maps.Clone(contents[start].(*TextContent).AdditionalProperties),
			},
			Text: mergeText(contents, start, end),
		}
	})

	contents = coalesce(contents, false,
		func(a, b *TextReasoningContent) bool { return a.ProtectedData == "" }, // we allow merging if the first item has no ProtectedData, even if the second does
		func(contents []Content, start, end int) *TextReasoningContent {
			content := &TextReasoningContent{
				ContentHeader: ContentHeader{
					AdditionalProperties: maps.Clone(contents[start].(*TextReasoningContent).AdditionalProperties),
				},
				Text: mergeText(contents, start, end),
			}
			last := contents[end-1].(*TextReasoningContent)
			if last.ProtectedData != "" {
				content.ProtectedData = last.ProtectedData
			}
			return content
		})

	contents = coalesce(contents, false,
		func(a, b *DataContent) bool {
			return strings.EqualFold(a.MediaType, b.MediaType) && a.TopLevelMediaType() == "text" && a.Name == b.Name
		},
		func(contents []Content, start, end int) *DataContent {
			first := contents[start].(*DataContent)

			return &DataContent{
				ContentHeader: ContentHeader{
					AdditionalProperties: maps.Clone(first.AdditionalProperties),
				},
				Name:      first.Name,
				MediaType: first.MediaType,
				Data:      mergeBase64(contents, start, end),
			}
		})

	return contents
}

func coalesce[T Content](contents []Content, mergeSingle bool, canMerge func(a, b T) bool, merge func([]Content, int, int) T) []Content {
	// Iterate through all of the items in the list looking for contiguous items that can be coalesced.
	start := 0
	tryAsCoalescable := func(c Content) (T, bool) {
		if tc, ok := c.(T); ok && len(tc.Header().Annotations) == 0 {
			return tc, true
		}
		var zero T
		return zero, false
	}
	for start < len(contents) {
		first, ok := tryAsCoalescable(contents[start])
		if !ok {
			start++
			continue
		}
		// Iterate until we find a non-coalescable item.
		i := start + 1
		prev := first
		for i < len(contents) {
			next, ok := tryAsCoalescable(contents[i])
			if !ok {
				break
			}
			if canMerge != nil && !canMerge(prev, next) {
				break
			}
			i++
			prev = next
		}
		// If there's only one item in the run, and we don't want to merge single items, skip it.
		if start == i-1 && !mergeSingle {
			start = i
			continue
		}
		// Store the replacement node and nil out all of the nodes that we coalesced.
		// We can then remove all coalesced nodes in one O(N) operation later.
		// Leave start positioned at the start of the next run.
		contents[start] = merge(contents, start, i)
		start++
		for start < i {
			contents[start] = nil
			start++
		}
	}
	return slices.DeleteFunc(contents, func(c Content) bool { return c == nil })
}
