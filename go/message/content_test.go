// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework/go/message"
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
			Arguments: map[string]any{"key": "value"},
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
		&message.FunctionApprovalRequestContent{
			ID: "approval-123",
			FunctionCall: &message.FunctionCallContent{
				CallID: "1",
			},
		},
		&message.FunctionApprovalResponseContent{
			ID:       "approval-123",
			Approved: true,
			FunctionCall: &message.FunctionCallContent{
				CallID: "1",
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
