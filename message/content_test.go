// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

// Test TextContent
func TestTextContent_String(t *testing.T) {
	tc := &message.TextContent{Text: "test content"}
	if tc.String() != "test content" {
		t.Errorf("expected 'test content', got %q", tc.String())
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
	contents := message.Contents{
		&message.TextContent{Text: "sample text"},
		&message.TextReasoningContent{Text: "sample reasoning"},
		&message.FunctionCallContent{
			Arguments: `{"key":"value"}`,
		},
		&message.FunctionResultContent{
			CallID: "call-123",
			Result: map[string]any{"key": "value"},
			Error:  errors.New("sample error"),
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
			FileID: "file-123",
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

func TestToolApprovalRequestContent_CreateResponseSnapshotsFunctionCall(t *testing.T) {
	toolCall := &message.FunctionCallContent{
		ContentHeader: message.ContentHeader{
			AdditionalProperties: map[string]any{"key": "value"},
		},
		CallID:    "call-1",
		Name:      "deploy",
		Arguments: `{"environment":"prod"}`,
	}
	request := &message.ToolApprovalRequestContent{
		ContentHeader: message.ContentHeader{
			AdditionalProperties: map[string]any{"request": "value"},
		},
		RequestID: "approval-1",
		ToolCall:  toolCall,
	}

	response := request.CreateResponse(true, "approved")

	toolCall.Name = "destroy"
	toolCall.Arguments = `{"environment":"dev"}`
	toolCall.AdditionalProperties["key"] = "changed"
	request.AdditionalProperties["request"] = "changed"

	responseToolCall, ok := response.ToolCall.(*message.FunctionCallContent)
	if !ok {
		t.Fatalf("expected FunctionCallContent, got %T", response.ToolCall)
	}
	if responseToolCall.Name != "deploy" {
		t.Fatalf("expected response tool name to be snapshotted, got %q", responseToolCall.Name)
	}
	if responseToolCall.Arguments != `{"environment":"prod"}` {
		t.Fatalf("expected response tool arguments to be snapshotted, got %q", responseToolCall.Arguments)
	}
	if responseToolCall.AdditionalProperties["key"] != "value" {
		t.Fatalf("expected response tool additional properties to be snapshotted, got %v", responseToolCall.AdditionalProperties["key"])
	}
	if response.AdditionalProperties["request"] != "value" {
		t.Fatalf("expected response additional properties to be snapshotted, got %v", response.AdditionalProperties["request"])
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

func TestToolApprovalRequestContent_CreateResponseSnapshotsMCPServerToolCall(t *testing.T) {
	toolCall := &message.MCPServerToolCallContent{
		ContentHeader: message.ContentHeader{
			AdditionalProperties: map[string]any{"key": "value"},
		},
		CallID:     "call-1",
		Name:       "lookup",
		ServerName: "server-a",
		Arguments:  `{"query":"alpha"}`,
	}
	request := &message.ToolApprovalRequestContent{
		RequestID: "approval-1",
		ToolCall:  toolCall,
	}

	response := request.CreateResponse(true, "approved")

	toolCall.Name = "delete"
	toolCall.ServerName = "server-b"
	toolCall.Arguments = `{"query":"beta"}`
	toolCall.AdditionalProperties["key"] = "changed"

	responseToolCall, ok := response.ToolCall.(*message.MCPServerToolCallContent)
	if !ok {
		t.Fatalf("expected MCPServerToolCallContent, got %T", response.ToolCall)
	}
	if responseToolCall.Name != "lookup" {
		t.Fatalf("expected response tool name to be snapshotted, got %q", responseToolCall.Name)
	}
	if responseToolCall.ServerName != "server-a" {
		t.Fatalf("expected response server name to be snapshotted, got %q", responseToolCall.ServerName)
	}
	if responseToolCall.Arguments != `{"query":"alpha"}` {
		t.Fatalf("expected response tool arguments to be snapshotted, got %q", responseToolCall.Arguments)
	}
	if responseToolCall.AdditionalProperties["key"] != "value" {
		t.Fatalf("expected response tool additional properties to be snapshotted, got %v", responseToolCall.AdditionalProperties["key"])
	}
}

func TestCoalesceContents(t *testing.T) {
	tests := []struct {
		name     string
		input    []message.Content
		expected []message.Content
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := message.CoalesceContents(tt.input)
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
