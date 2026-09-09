// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/message"
)

var (
	_ message.ToolCallContent = (*message.FunctionCallContent)(nil)
	_ message.ToolCallContent = (*message.MCPServerToolCallContent)(nil)
	_ message.ToolCallContent = (*message.ImageGenerationToolCallContent)(nil)
	_ message.ToolCallContent = (*message.CodeInterpreterToolCallContent)(nil)
	_ message.ToolCallContent = (*message.WebSearchToolCallContent)(nil)

	_ message.ToolResultContent = (*message.FunctionResultContent)(nil)
	_ message.ToolResultContent = (*message.MCPServerToolResultContent)(nil)
	_ message.ToolResultContent = (*message.ImageGenerationToolResultContent)(nil)
	_ message.ToolResultContent = (*message.CodeInterpreterToolResultContent)(nil)
	_ message.ToolResultContent = (*message.WebSearchToolResultContent)(nil)

	_ message.InputRequestContent  = (*message.ToolApprovalRequestContent)(nil)
	_ message.InputResponseContent = (*message.ToolApprovalResponseContent)(nil)
)

// Test TextContent
func TestTextContent_String(t *testing.T) {
	tc := &message.TextContent{Text: "test content"}
	if tc.String() != "test content" {
		t.Errorf("expected 'test content', got %q", tc.String())
	}
}

func TestContentsText(t *testing.T) {
	tests := []struct {
		name     string
		contents message.Contents
		want     string
	}{
		{
			name:     "empty",
			contents: message.Contents{},
			want:     "",
		},
		{
			name: "concatenates all text contents in order",
			contents: message.Contents{
				&message.TextContent{Text: "foo"},
				&message.TextReasoningContent{Text: "ignored"},
				&message.TextContent{Text: "bar"},
			},
			want: "foobar",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.contents.Text(); got != tt.want {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Test TextReasoningContent
func TestTextReasoningContent_String(t *testing.T) {
	trc := &message.TextReasoningContent{Text: "reasoning text"}
	if trc.String() != "reasoning text" {
		t.Errorf("expected 'reasoning text', got %q", trc.String())
	}
}

// Test UsageDetails
func TestUsageDetails_Add(t *testing.T) {
	ud1 := message.UsageDetails{
		InputTokenCount:  10,
		OutputTokenCount: 20,
		TotalTokenCount:  30,
	}
	ud2 := message.UsageDetails{
		InputTokenCount:  5,
		OutputTokenCount: 15,
		TotalTokenCount:  20,
	}

	ud1.Add(ud2)
	if ud1.InputTokenCount != 15 {
		t.Errorf("expected input tokens 15, got %d", ud1.InputTokenCount)
	}
	if ud1.OutputTokenCount != 35 {
		t.Errorf("expected output tokens 35, got %d", ud1.OutputTokenCount)
	}
	if ud1.TotalTokenCount != 50 {
		t.Errorf("expected total tokens 50, got %d", ud1.TotalTokenCount)
	}
}

func TestUsageDetails_AddWithAdditionalCounts(t *testing.T) {
	ud1 := message.UsageDetails{
		InputTokenCount: 10,
		AdditionalCounts: map[string]int64{
			"cache_read": 5,
		},
	}
	ud2 := message.UsageDetails{
		InputTokenCount: 5,
		AdditionalCounts: map[string]int64{
			"cache_read":  3,
			"cache_write": 2,
		},
	}

	ud1.Add(ud2)
	if ud1.AdditionalCounts["cache_read"] != 8 {
		t.Errorf("expected cache_read 8, got %d", ud1.AdditionalCounts["cache_read"])
	}
	if ud1.AdditionalCounts["cache_write"] != 2 {
		t.Errorf("expected cache_write 2, got %d", ud1.AdditionalCounts["cache_write"])
	}
}

func TestContentEncoding_Roundtrip(t *testing.T) {
	createdAt := time.Date(2026, time.September, 9, 12, 30, 0, 0, time.UTC)
	sizeInBytes := int64(1024)
	contents := message.Contents{
		&message.TextContent{Text: "sample text"},
		&message.TextReasoningContent{Text: "sample reasoning"},
		&message.FunctionCallContent{
			Arguments: `{"key":"value"}`,
		},
		&message.FunctionResultContent{
			CallID: "call-123",
			Result: map[string]any{"key": "value"},
		},
		&message.URIContent{
			URI: "https://example.com/resource",
		},
		&message.UsageContent{
			Details: message.UsageDetails{
				InputTokenCount:  10,
				OutputTokenCount: 20,
				TotalTokenCount:  30,
			},
		},
		&message.ErrorContent{
			ErrorCode: "1",
			Message:   "sample error message",
			Details:   "sample error details",
		},
		&message.DataContent{
			Data:      base64.StdEncoding.EncodeToString([]byte("sample data")),
			Name:      "sample data name",
			MediaType: "text/plain",
		},
		&message.HostedFileContent{
			FileID:      "file-123",
			Name:        "document.txt",
			MediaType:   "text/plain",
			SizeInBytes: &sizeInBytes,
			CreatedAt:   &createdAt,
		},
		&message.HostedVectorStoreContent{
			VectorStoreID: "store-123",
		},
		&message.ToolApprovalRequestContent{
			RequestID: "approval-123",
			ToolCall: &message.FunctionCallContent{
				CallID: "1",
			},
		},
		&message.ToolApprovalResponseContent{
			RequestID: "approval-123",
			Approved:  true,
			ToolCall: &message.FunctionCallContent{
				CallID: "1",
			},
		},
		&message.ToolApprovalRequestContent{
			RequestID: "mcp-approval-123",
			ToolCall: &message.MCPServerToolCallContent{
				CallID:     "mcp-call-123",
				Arguments:  "{\"arg1\":\"value1\"}",
				Name:       "mcpName",
				ServerName: "mcpServer",
			},
		},
		&message.ToolApprovalResponseContent{
			RequestID: "mcp-approval-123",
			Approved:  true,
			ToolCall: &message.MCPServerToolCallContent{
				CallID:     "mcp-call-123",
				Arguments:  "{\"arg1\":\"value1\"}",
				Name:       "mcpName",
				ServerName: "mcpServer",
			},
		},
		&message.AlwaysApproveToolApprovalResponseContent{
			InnerResponse: &message.ToolApprovalResponseContent{
				RequestID: "approval-124",
				Approved:  true,
				ToolCall: &message.FunctionCallContent{
					CallID: "2",
					Name:   "deploy",
				},
			},
			AlwaysApproveToolWithArguments: true,
		},
		&message.MCPServerToolCallContent{
			CallID:     "mcp-call-123",
			Arguments:  "{\"arg1\":\"value1\"}",
			Name:       "mcpName",
			ServerName: "mcpServer",
		},
		&message.MCPServerToolResultContent{
			CallID: "mcp-call-123",
			Outputs: message.Contents{
				&message.TextContent{Text: "mcp tool output"},
			},
		},
		&message.ImageGenerationToolCallContent{
			CallID: "image-call-123",
		},
		&message.ImageGenerationToolResultContent{
			CallID: "image-call-123",
			Outputs: message.Contents{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("image")),
					MediaType: "image/png",
				},
			},
		},
		&message.WebSearchToolCallContent{
			CallID:  "web-call-123",
			Queries: []string{"first query", "second query"},
		},
		&message.WebSearchToolResultContent{
			CallID: "web-call-123",
			Outputs: message.Contents{
				&message.URIContent{
					URI:       "https://example.com/result",
					MediaType: "text/html",
				},
			},
		},
	}
	data, err := json.Marshal(contents)
	if err != nil {
		t.Fatal(err)
	}
	var decoded message.Contents
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(contents) {
		t.Fatalf("expected %d contents, got %d", len(contents), len(decoded))
	}
	for i, v := range contents {
		if !reflect.DeepEqual(v, decoded[i]) {
			t.Errorf("[%d]: expected content %v, got %v", i, v, decoded[i])
		}
	}
}

func TestFunctionCallContent_ErrorNotSerialized(t *testing.T) {
	content := &message.FunctionCallContent{
		CallID:    "call-1",
		Name:      "doThing",
		Arguments: `{"a":1}`,
		Error:     errors.New("mapping failed"),
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["Error"]; ok {
		t.Fatalf("Error must not be serialized, got %s", data)
	}

	var decoded message.FunctionCallContent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error != nil {
		t.Fatalf("Error must be nil after unmarshal, got %v", decoded.Error)
	}
}

func TestFunctionResultContent_ErrorNotSerialized(t *testing.T) {
	content := &message.FunctionResultContent{
		CallID: "call-1",
		Result: "ok",
		Error:  errors.New("function failed"),
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["Error"]; ok {
		t.Fatalf("Error must not be serialized, got %s", data)
	}

	var decoded message.FunctionResultContent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error != nil {
		t.Fatalf("Error must be nil after unmarshal, got %v", decoded.Error)
	}
}

func TestFunctionContentUnmarshal_ClearsStaleError(t *testing.T) {
	tests := []struct {
		name   string
		data   string
		target any
	}{
		{
			name:   "function call omitted error",
			data:   `{"Arguments":"{}","CallID":"call","Name":"tool","InformationalOnly":false,"Type":"functionCall"}`,
			target: &message.FunctionCallContent{Error: errors.New("stale")},
		},
		{
			name:   "function result empty error",
			data:   `{"CallID":"call","Error":"","Result":"ok","Type":"functionResult"}`,
			target: &message.FunctionResultContent{Error: errors.New("stale")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(test.data), test.target); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			switch target := test.target.(type) {
			case *message.FunctionCallContent:
				if target.Error != nil {
					t.Fatalf("Error = %v, want nil", target.Error)
				}
			case *message.FunctionResultContent:
				if target.Error != nil {
					t.Fatalf("Error = %v, want nil", target.Error)
				}
			}
		})
	}
}

func TestContentEncoding_PreservesAdditionalProperties(t *testing.T) {
	original := &message.TextContent{
		ContentHeader: message.ContentHeader{
			AdditionalProperties: map[string]any{"provider": "openai", "region": "eastus"},
		},
		Text: "sample text",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("AdditionalProperties")) {
		t.Fatalf("marshaled JSON does not contain AdditionalProperties: %s", data)
	}
	var decoded message.TextContent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original.AdditionalProperties, decoded.AdditionalProperties) {
		t.Fatalf("AdditionalProperties = %v, want %v", decoded.AdditionalProperties, original.AdditionalProperties)
	}
}

func TestCodeInterpreterContentEncoding_Roundtrip(t *testing.T) {
	tests := []struct {
		name    string
		content message.Content
	}{
		{
			name: "toolCall",
			content: &message.CodeInterpreterToolCallContent{
				CallID: "code-call-123",
				Inputs: message.Contents{
					&message.TextContent{Text: "print('hello')"},
				},
			},
		},
		{
			name: "toolResult",
			content: &message.CodeInterpreterToolResultContent{
				CallID: "code-call-123",
				Outputs: message.Contents{
					&message.TextContent{Text: "hello"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(message.Contents{tt.content})
			if err != nil {
				t.Fatal(err)
			}
			var decoded message.Contents
			if err = json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded) != 1 {
				t.Fatalf("expected 1 content, got %d", len(decoded))
			}
			if _, ok := decoded[0].(*message.RawContent); ok {
				t.Fatalf("content decoded to *message.RawContent, want %T", tt.content)
			}
			if !reflect.DeepEqual(tt.content, decoded[0]) {
				t.Errorf("expected content %v, got %v", tt.content, decoded[0])
			}
		})
	}
}

func TestCodeInterpreterToolCallContentImplementsToolCallContent(t *testing.T) {
	var content message.ToolCallContent = &message.CodeInterpreterToolCallContent{CallID: "call-123"}
	if content.GetCallID() != "call-123" {
		t.Fatalf("GetCallID() = %q, want call-123", content.GetCallID())
	}
}

func TestToolApprovalContentEncoding_RoundtripsStableToolCalls(t *testing.T) {
	toolCalls := []message.ToolCallContent{
		&message.FunctionCallContent{CallID: "function-call", Name: "lookup"},
		&message.MCPServerToolCallContent{CallID: "mcp-call", Name: "lookup"},
		&message.ImageGenerationToolCallContent{CallID: "image-call"},
		&message.CodeInterpreterToolCallContent{
			CallID: "code-call",
			Inputs: message.Contents{&message.TextContent{Text: "print('hello')"}},
		},
		&message.WebSearchToolCallContent{CallID: "web-call", Queries: []string{"query"}},
	}

	for _, toolCall := range toolCalls {
		t.Run(toolCall.GetCallID(), func(t *testing.T) {
			request := &message.ToolApprovalRequestContent{
				RequestID: "request-" + toolCall.GetCallID(),
				ToolCall:  toolCall,
			}
			contents := message.Contents{request, request.CreateResponse(true, "approved")}
			data, err := json.Marshal(contents)
			if err != nil {
				t.Fatal(err)
			}
			var decoded message.Contents
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(contents, decoded) {
				t.Fatalf("decoded = %#v, want %#v", decoded, contents)
			}
		})
	}
}

func TestDataContentUnmarshalDefaultsMissingMediaType(t *testing.T) {
	var content message.DataContent
	if err := json.Unmarshal([]byte(`{"Type":"data","URI":"data:,hello%20world+literal"}`), &content); err != nil {
		t.Fatal(err)
	}
	if content.MediaType != "text/plain;charset=US-ASCII" {
		t.Fatalf("MediaType = %q, want text/plain;charset=US-ASCII", content.MediaType)
	}
	data, err := content.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world+literal" {
		t.Fatalf("data = %q, want hello world+literal", string(data))
	}
}

func TestNewDataContent(t *testing.T) {
	content, err := message.NewDataContent([]byte("hello"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if content.Data != "aGVsbG8=" || content.MediaType != "text/plain" {
		t.Fatalf("content = %#v", content)
	}
	data, err := content.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("Bytes() = %q, want hello", data)
	}
	if _, err := message.NewDataContent(nil, "invalid media type"); err == nil {
		t.Fatal("NewDataContent() error = nil, want invalid media type error")
	}
}

func TestNewDataContentFromURI(t *testing.T) {
	content, err := message.NewDataContentFromURI("data:,hello%20world", "")
	if err != nil {
		t.Fatal(err)
	}
	if content.MediaType != "text/plain;charset=US-ASCII" {
		t.Fatalf("MediaType = %q", content.MediaType)
	}
	data, err := content.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("Bytes() = %q, want hello world", data)
	}
}

func TestDataContentMarshalRejectsInvalidValue(t *testing.T) {
	tests := []struct {
		name    string
		content *message.DataContent
	}{
		{name: "invalid base64", content: &message.DataContent{MediaType: "text/plain", Data: "%%%"}},
		{name: "invalid media type", content: &message.DataContent{MediaType: "not a media type", Data: "aGVsbG8="}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := json.Marshal(test.content); err == nil {
				t.Fatal("Marshal() error = nil, want validation error")
			}
		})
	}
}

func TestDataContentUnmarshalPreservesInvalidPercentEscapes(t *testing.T) {
	var content message.DataContent
	if err := json.Unmarshal([]byte(`{"Type":"data","URI":"data:,hello%20%ZZ+there"}`), &content); err != nil {
		t.Fatal(err)
	}
	data, err := content.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello%20%ZZ+there" {
		t.Fatalf("data = %q, want original invalid URL data", string(data))
	}
}

func TestNewURIContentInfersMediaType(t *testing.T) {
	content, err := message.NewURIContent("https://example.com/images/chart.png?size=large", "")
	if err != nil {
		t.Fatal(err)
	}
	if content.MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", content.MediaType)
	}

	content, err = message.NewURIContent("https://example.com/download", "")
	if err != nil {
		t.Fatal(err)
	}
	if content.MediaType != "application/octet-stream" {
		t.Fatalf("MediaType = %q, want application/octet-stream", content.MediaType)
	}

	content, err = message.NewURIContent("data:image/png;base64,iVBORw0KGgo=", "")
	if err != nil {
		t.Fatal(err)
	}
	if content.MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", content.MediaType)
	}
}

func TestNewURIContentValidatesURI(t *testing.T) {
	if _, err := message.NewURIContent("relative/path.png", ""); err == nil {
		t.Fatal("expected relative URI to fail")
	}
	if _, err := message.NewURIContent("https://exa mple.com/image.png", ""); err == nil {
		t.Fatal("expected invalid host URI to fail")
	}
	if _, err := message.NewURIContent("https://example.com/%ZZ.png", ""); err == nil {
		t.Fatal("expected invalid percent escape URI to fail")
	}
}

func TestNewURIContentUsesExplicitMediaType(t *testing.T) {
	content, err := message.NewURIContent("https://example.com/image.png", "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if content.MediaType != "image/jpeg" {
		t.Fatalf("MediaType = %q, want image/jpeg", content.MediaType)
	}
	if _, err := message.NewURIContent("https://example.com/image.png", "not a media type"); err == nil {
		t.Fatal("expected invalid media type to fail")
	}
}

func TestContentHasTopLevelMediaType(t *testing.T) {
	tests := []struct {
		name    string
		content interface{ HasTopLevelMediaType(string) bool }
		want    string
		missing string
	}{
		{name: "data", content: &message.DataContent{MediaType: " Image/PNG "}, want: "image", missing: "text"},
		{name: "hosted file", content: &message.HostedFileContent{MediaType: "TEXT/PLAIN"}, want: "text", missing: "image"},
		{name: "URI", content: &message.URIContent{MediaType: "application/json"}, want: "APPLICATION", missing: "text"},
		{name: "unset", content: &message.DataContent{}, want: "image", missing: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.content.HasTopLevelMediaType(test.want); got != (test.name != "unset") {
				t.Fatalf("HasTopLevelMediaType(%q) = %v", test.want, got)
			}
			if test.content.HasTopLevelMediaType(test.missing) {
				t.Fatalf("HasTopLevelMediaType(%q) = true, want false", test.missing)
			}
		})
	}
}

func TestContentEncoding_UnmarshalMissingTypeUsesRawContent(t *testing.T) {
	const rawContent = `{"Provider":"github","Payload":{"value":42}}`
	data := []byte(`[` + rawContent + `]`)

	var contents message.Contents
	if err := json.Unmarshal(data, &contents); err != nil {
		t.Fatal(err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	raw, ok := contents[0].(*message.RawContent)
	if !ok {
		t.Fatalf("content = %T, want *message.RawContent", contents[0])
	}
	if got := string(raw.Header().RawRepresentation.(json.RawMessage)); got != rawContent {
		t.Fatalf("RawRepresentation = %s, want %s", got, rawContent)
	}
}

func TestContentEncoding_UnmarshalUnknownTypeUsesRawContent(t *testing.T) {
	const rawContent = `{"Type":"futureContent","Value":42}`
	data := []byte(`[` + rawContent + `]`)

	var contents message.Contents
	if err := json.Unmarshal(data, &contents); err != nil {
		t.Fatal(err)
	}
	raw, ok := contents[0].(*message.RawContent)
	if !ok {
		t.Fatalf("content = %T, want *message.RawContent", contents[0])
	}
	if got := string(raw.Header().RawRepresentation.(json.RawMessage)); got != rawContent {
		t.Fatalf("RawRepresentation = %s, want %s", got, rawContent)
	}

	encoded, err := json.Marshal(contents)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); got != string(data) {
		t.Fatalf("encoded = %s, want %s", got, data)
	}
}

func TestContentEncoding_RawContentMarshalHasNoType(t *testing.T) {
	data, err := json.Marshal(message.Contents{&message.RawContent{}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `[{}]`; got != want {
		t.Fatalf("encoded = %s, want %s", got, want)
	}
}

func TestToolApprovalRequestContent_CreateResponseUsesToolCallReference(t *testing.T) {
	toolCalls := []message.ToolCallContent{
		&message.FunctionCallContent{CallID: "function-call", Name: "lookup"},
		&message.MCPServerToolCallContent{CallID: "mcp-call", Name: "lookup"},
		&message.ImageGenerationToolCallContent{CallID: "image-call"},
		&message.CodeInterpreterToolCallContent{CallID: "code-call"},
		&message.WebSearchToolCallContent{CallID: "web-call"},
	}

	for _, toolCall := range toolCalls {
		t.Run(toolCall.GetCallID(), func(t *testing.T) {
			request := &message.ToolApprovalRequestContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: map[string]any{"request": "value"},
					Annotations:          message.Annotations{&message.CitationAnnotation{Title: "source"}},
					RawRepresentation:    "raw-request",
				},
				RequestID: "approval-1",
				ToolCall:  toolCall,
			}

			response := request.CreateResponse(true, "approved")

			if response.RequestID != request.RequestID || !response.Approved || response.Reason != "approved" {
				t.Fatalf("response = %#v", response)
			}
			if response.ToolCall != toolCall {
				t.Fatalf("ToolCall = %p, want original %p", response.ToolCall, toolCall)
			}
			if response.AdditionalProperties != nil || response.Annotations != nil || response.RawRepresentation != nil {
				t.Fatalf("ContentHeader = %#v, want zero value", response.ContentHeader)
			}
		})
	}
}

func TestToolApprovalRequestContent_AlwaysApproveSnapshotsAdditionalProperties(t *testing.T) {
	newRequest := func() *message.ToolApprovalRequestContent {
		return &message.ToolApprovalRequestContent{
			ContentHeader: message.ContentHeader{
				AdditionalProperties: map[string]any{"request": "value"},
			},
			RequestID: "approval-1",
			ToolCall: &message.FunctionCallContent{
				CallID: "call-1",
				Name:   "deploy",
			},
		}
	}

	t.Run("AlwaysApproveToolResponse", func(t *testing.T) {
		request := newRequest()
		response := request.AlwaysApproveToolResponse()
		request.AdditionalProperties["request"] = "changed"
		if response.AdditionalProperties["request"] != "value" {
			t.Fatalf("expected response additional properties to be snapshotted, got %v", response.AdditionalProperties["request"])
		}
	})

	t.Run("AlwaysApproveToolWithArgumentsResponse", func(t *testing.T) {
		request := newRequest()
		response := request.AlwaysApproveToolWithArgumentsResponse()
		request.AdditionalProperties["request"] = "changed"
		if response.AdditionalProperties["request"] != "value" {
			t.Fatalf("expected response additional properties to be snapshotted, got %v", response.AdditionalProperties["request"])
		}
	})
}

func TestContentsCoalesce(t *testing.T) {
	tests := []struct {
		name     string
		input    message.Contents
		expected message.Contents
	}{
		{
			name:     "empty list",
			input:    []message.Content{},
			expected: []message.Content{},
		},
		{
			name: "single text content",
			input: []message.Content{
				&message.TextContent{Text: "hello"},
			},
			expected: []message.Content{
				&message.TextContent{Text: "hello"},
			},
		},
		{
			name: "multiple consecutive text contents",
			input: []message.Content{
				&message.TextContent{Text: "hello"},
				&message.TextContent{Text: " "},
				&message.TextContent{Text: "world"},
			},
			expected: []message.Content{
				&message.TextContent{Text: "hello world"},
			},
		},
		{
			name: "text contents with additional properties",
			input: []message.Content{
				&message.TextContent{
					ContentHeader: message.ContentHeader{
						AdditionalProperties: map[string]any{"key": "value"},
					},
					Text: "hello",
				},
				&message.TextContent{Text: " world"},
			},
			expected: []message.Content{
				&message.TextContent{
					ContentHeader: message.ContentHeader{
						AdditionalProperties: map[string]any{"key": "value"},
					},
					Text: "hello world",
				},
			},
		},
		{
			name: "text contents with annotations are not coalesced",
			input: []message.Content{
				&message.TextContent{
					ContentHeader: message.ContentHeader{
						Annotations: message.Annotations{
							&message.CitationAnnotation{Title: "source"},
						},
					},
					Text: "hello",
				},
				&message.TextContent{Text: " world"},
			},
			expected: []message.Content{
				&message.TextContent{
					ContentHeader: message.ContentHeader{
						Annotations: message.Annotations{
							&message.CitationAnnotation{Title: "source"},
						},
					},
					Text: "hello",
				},
				&message.TextContent{Text: " world"},
			},
		},
		{
			name: "multiple consecutive text reasoning contents",
			input: []message.Content{
				&message.TextReasoningContent{Text: "thinking"},
				&message.TextReasoningContent{Text: " hard"},
			},
			expected: []message.Content{
				&message.TextReasoningContent{Text: "thinking hard"},
			},
		},
		{
			name: "text reasoning with protected data preserves last",
			input: []message.Content{
				&message.TextReasoningContent{Text: "part1"},
				&message.TextReasoningContent{
					Text:          "part2",
					ProtectedData: "protected",
				},
			},
			expected: []message.Content{
				&message.TextReasoningContent{
					Text:          "part1part2",
					ProtectedData: "protected",
				},
			},
		},
		{
			name: "text reasoning with first protected data not coalesced",
			input: []message.Content{
				&message.TextReasoningContent{
					Text:          "part1",
					ProtectedData: "protected1",
				},
				&message.TextReasoningContent{Text: "part2"},
			},
			expected: []message.Content{
				&message.TextReasoningContent{
					Text:          "part1",
					ProtectedData: "protected1",
				},
				&message.TextReasoningContent{Text: "part2"},
			},
		},
		{
			name: "data contents with same text media type",
			input: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("hello")),
					MediaType: "text/plain",
				},
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte(" world")),
					MediaType: "text/plain",
				},
			},
			expected: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("hello world")),
					MediaType: "text/plain",
				},
			},
		},
		{
			name: "data contents with different media types not coalesced",
			input: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("hello")),
					MediaType: "text/plain",
				},
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("world")),
					MediaType: "text/html",
				},
			},
			expected: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("hello")),
					MediaType: "text/plain",
				},
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("world")),
					MediaType: "text/html",
				},
			},
		},
		{
			name: "data contents with differently cased media types not coalesced",
			input: []message.Content{
				&message.DataContent{Data: base64.StdEncoding.EncodeToString([]byte("hello")), MediaType: "text/plain"},
				&message.DataContent{Data: base64.StdEncoding.EncodeToString([]byte(" world")), MediaType: "TEXT/PLAIN"},
			},
			expected: []message.Content{
				&message.DataContent{Data: base64.StdEncoding.EncodeToString([]byte("hello")), MediaType: "text/plain"},
				&message.DataContent{Data: base64.StdEncoding.EncodeToString([]byte(" world")), MediaType: "TEXT/PLAIN"},
			},
		},
		{
			name: "data contents with non-text media type not coalesced",
			input: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
					MediaType: "application/octet-stream",
				},
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte{0x03, 0x04}),
					MediaType: "application/octet-stream",
				},
			},
			expected: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}),
					MediaType: "application/octet-stream",
				},
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte{0x03, 0x04}),
					MediaType: "application/octet-stream",
				},
			},
		},
		{
			name: "mixed content types not coalesced",
			input: []message.Content{
				&message.TextContent{Text: "hello"},
				&message.FunctionCallContent{CallID: "call-1"},
				&message.TextContent{Text: "world"},
			},
			expected: []message.Content{
				&message.TextContent{Text: "hello"},
				&message.FunctionCallContent{CallID: "call-1"},
				&message.TextContent{Text: "world"},
			},
		},
		{
			name: "complex scenario with multiple types",
			input: []message.Content{
				&message.TextContent{Text: "start"},
				&message.TextContent{Text: " middle"},
				&message.FunctionCallContent{CallID: "call-1"},
				&message.TextReasoningContent{Text: "think1"},
				&message.TextReasoningContent{Text: " think2"},
				&message.TextContent{Text: "end1"},
				&message.TextContent{Text: " end2"},
			},
			expected: []message.Content{
				&message.TextContent{Text: "start middle"},
				&message.FunctionCallContent{CallID: "call-1"},
				&message.TextReasoningContent{Text: "think1 think2"},
				&message.TextContent{Text: "end1 end2"},
			},
		},
		{
			name: "data contents with different names not coalesced",
			input: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("hello")),
					MediaType: "text/plain",
					Name:      "file1.txt",
				},
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte(" world")),
					MediaType: "text/plain",
					Name:      "file2.txt",
				},
			},
			expected: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("hello")),
					MediaType: "text/plain",
					Name:      "file1.txt",
				},
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte(" world")),
					MediaType: "text/plain",
					Name:      "file2.txt",
				},
			},
		},
		{
			name: "data contents with same name coalesced",
			input: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("hello")),
					MediaType: "text/plain",
					Name:      "file.txt",
				},
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte(" world")),
					MediaType: "text/plain",
					Name:      "file.txt",
				},
			},
			expected: []message.Content{
				&message.DataContent{
					Data:      base64.StdEncoding.EncodeToString([]byte("hello world")),
					MediaType: "text/plain",
					Name:      "file.txt",
				},
			},
		},
		{
			name: "code interpreter tool call with same call id coalesced and inputs merged",
			input: []message.Content{
				&message.CodeInterpreterToolCallContent{
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "hello"},
						&message.TextContent{Text: " world"},
					},
				},
				&message.CodeInterpreterToolCallContent{
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "!"},
					},
				},
			},
			expected: []message.Content{
				&message.CodeInterpreterToolCallContent{
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "hello world!"},
					},
				},
			},
		},
		{
			name: "code interpreter tool call with different call ids not coalesced but inputs still merged",
			input: []message.Content{
				&message.CodeInterpreterToolCallContent{
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "a"},
						&message.TextContent{Text: "b"},
					},
				},
				&message.CodeInterpreterToolCallContent{
					CallID: "call-2",
					Inputs: message.Contents{
						&message.TextContent{Text: "c"},
						&message.TextContent{Text: "d"},
					},
				},
			},
			expected: []message.Content{
				&message.CodeInterpreterToolCallContent{
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "ab"},
					},
				},
				&message.CodeInterpreterToolCallContent{
					CallID: "call-2",
					Inputs: message.Contents{
						&message.TextContent{Text: "cd"},
					},
				},
			},
		},
		{
			name: "single code interpreter tool call still coalesces nested inputs",
			input: []message.Content{
				&message.CodeInterpreterToolCallContent{
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "foo"},
						&message.TextContent{Text: "bar"},
					},
				},
			},
			expected: []message.Content{
				&message.CodeInterpreterToolCallContent{
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "foobar"},
					},
				},
			},
		},
		{
			name: "single code interpreter tool call preserves raw representation",
			input: []message.Content{
				&message.CodeInterpreterToolCallContent{
					ContentHeader: message.ContentHeader{
						RawRepresentation: "raw-call",
					},
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "foo"},
						&message.TextContent{Text: "bar"},
					},
				},
			},
			expected: []message.Content{
				&message.CodeInterpreterToolCallContent{
					ContentHeader: message.ContentHeader{
						RawRepresentation: "raw-call",
					},
					CallID: "call-1",
					Inputs: message.Contents{
						&message.TextContent{Text: "foobar"},
					},
				},
			},
		},
		{
			name: "code interpreter tool result with same call id coalesced and outputs merged",
			input: []message.Content{
				&message.CodeInterpreterToolResultContent{
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "res"},
						&message.TextContent{Text: "ult"},
					},
				},
				&message.CodeInterpreterToolResultContent{
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "!"},
					},
				},
			},
			expected: []message.Content{
				&message.CodeInterpreterToolResultContent{
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "result!"},
					},
				},
			},
		},
		{
			name: "code interpreter tool result with different call ids not coalesced but outputs still merged",
			input: []message.Content{
				&message.CodeInterpreterToolResultContent{
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "a"},
						&message.TextContent{Text: "b"},
					},
				},
				&message.CodeInterpreterToolResultContent{
					CallID: "call-2",
					Outputs: message.Contents{
						&message.TextContent{Text: "c"},
						&message.TextContent{Text: "d"},
					},
				},
			},
			expected: []message.Content{
				&message.CodeInterpreterToolResultContent{
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "ab"},
					},
				},
				&message.CodeInterpreterToolResultContent{
					CallID: "call-2",
					Outputs: message.Contents{
						&message.TextContent{Text: "cd"},
					},
				},
			},
		},
		{
			name: "single code interpreter tool result still coalesces nested outputs",
			input: []message.Content{
				&message.CodeInterpreterToolResultContent{
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "foo"},
						&message.TextContent{Text: "bar"},
					},
				},
			},
			expected: []message.Content{
				&message.CodeInterpreterToolResultContent{
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "foobar"},
					},
				},
			},
		},
		{
			name: "single code interpreter tool result preserves raw representation",
			input: []message.Content{
				&message.CodeInterpreterToolResultContent{
					ContentHeader: message.ContentHeader{
						RawRepresentation: "raw-result",
					},
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "foo"},
						&message.TextContent{Text: "bar"},
					},
				},
			},
			expected: []message.Content{
				&message.CodeInterpreterToolResultContent{
					ContentHeader: message.ContentHeader{
						RawRepresentation: "raw-result",
					},
					CallID: "call-1",
					Outputs: message.Contents{
						&message.TextContent{Text: "foobar"},
					},
				},
			},
		},
		{
			name: "image generation results with the same call id keep the latest result",
			input: []message.Content{
				&message.ImageGenerationToolResultContent{
					CallID:  "image-1",
					Outputs: message.Contents{&message.TextContent{Text: "partial"}},
				},
				&message.TextContent{Text: "between"},
				&message.ImageGenerationToolResultContent{
					CallID:  "image-2",
					Outputs: message.Contents{&message.TextContent{Text: "other"}},
				},
				&message.ImageGenerationToolResultContent{
					ContentHeader: message.ContentHeader{AdditionalProperties: map[string]any{"final": true}},
					CallID:        "image-1",
					Outputs:       message.Contents{&message.TextContent{Text: "complete"}},
				},
			},
			expected: []message.Content{
				&message.ImageGenerationToolResultContent{
					ContentHeader: message.ContentHeader{AdditionalProperties: map[string]any{"final": true}},
					CallID:        "image-1",
					Outputs:       message.Contents{&message.TextContent{Text: "complete"}},
				},
				&message.TextContent{Text: "between"},
				&message.ImageGenerationToolResultContent{
					CallID:  "image-2",
					Outputs: message.Contents{&message.TextContent{Text: "other"}},
				},
			},
		},
		{
			name: "web search calls with the same call id merge queries and metadata",
			input: []message.Content{
				&message.WebSearchToolCallContent{CallID: "web-1", Queries: []string{"first query"}},
				&message.TextContent{Text: "between"},
				&message.WebSearchToolCallContent{CallID: "web-2", Queries: []string{"other query"}},
				&message.WebSearchToolCallContent{
					ContentHeader: message.ContentHeader{
						AdditionalProperties: map[string]any{"provider": "search"},
						RawRepresentation:    "raw-search-call",
					},
					CallID:  "web-1",
					Queries: []string{"second query"},
				},
			},
			expected: []message.Content{
				&message.WebSearchToolCallContent{
					ContentHeader: message.ContentHeader{
						AdditionalProperties: map[string]any{"provider": "search"},
						RawRepresentation:    "raw-search-call",
					},
					CallID:  "web-1",
					Queries: []string{"first query", "second query"},
				},
				&message.TextContent{Text: "between"},
				&message.WebSearchToolCallContent{CallID: "web-2", Queries: []string{"other query"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.input.Coalesce()
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d contents, got %d", len(tt.expected), len(result))
			}
			for i := range result {
				if !reflect.DeepEqual(result[i], tt.expected[i]) {
					t.Errorf("[%d]: expected %#v, got %#v", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

func TestContentsCoalesce_PreservesInvalidDataContent(t *testing.T) {
	invalid := &message.DataContent{Data: "%%%", MediaType: "text/plain"}
	valid := &message.DataContent{Data: base64.StdEncoding.EncodeToString([]byte("valid")), MediaType: "text/plain"}

	got := (message.Contents{invalid, valid}).Coalesce()
	if len(got) != 2 || got[0] != invalid || got[1] != valid {
		t.Fatalf("Contents.Coalesce() = %#v, want original contents", got)
	}
}

func TestContentsCoalesce_PreservesSingleCodeInterpreterContentIdentity(t *testing.T) {
	call := &message.CodeInterpreterToolCallContent{
		CallID: "call-1",
		Inputs: message.Contents{
			&message.TextContent{Text: "a"},
			&message.TextContent{Text: "b"},
		},
	}
	result := &message.CodeInterpreterToolResultContent{
		CallID: "call-1",
		Outputs: message.Contents{
			&message.TextContent{Text: "c"},
			&message.TextContent{Text: "d"},
		},
	}

	got := (message.Contents{call, result}).Coalesce()
	if got[0] != call || got[1] != result {
		t.Fatalf("Contents.Coalesce() replaced single code-interpreter content")
	}
	if call.Inputs.Text() != "ab" || result.Outputs.Text() != "cd" {
		t.Fatalf("nested contents = %q, %q", call.Inputs.Text(), result.Outputs.Text())
	}
}
