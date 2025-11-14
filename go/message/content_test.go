// Copyright (c) Microsoft. All rights reserved.

package message_test

import (
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
	ud1 := &message.UsageDetails{
		InputTokenCount:  10,
		OutputTokenCount: 20,
		TotalTokenCount:  30,
	}
	ud2 := &message.UsageDetails{
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
	ud1 := &message.UsageDetails{
		InputTokenCount: 10,
		AdditionalCounts: map[string]int64{
			"cache_read": 5,
		},
	}
	ud2 := &message.UsageDetails{
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

func TestUsageDetails_AddWithNil(t *testing.T) {
	var ud1 *message.UsageDetails
	ud2 := &message.UsageDetails{InputTokenCount: 10}

	// Should not panic
	ud1.Add(ud2)

	ud1 = &message.UsageDetails{InputTokenCount: 10}
	ud1.Add(nil)
	if ud1.InputTokenCount != 10 {
		t.Errorf("expected input tokens to remain 10, got %d", ud1.InputTokenCount)
	}
}

func TestContentEncoding_Roundtrip(t *testing.T) {
	contents := message.Contents{
		&message.TextContent{Text: "sample text"},
		&message.TextReasoningContent{Text: "sample reasoning"},
		&message.FunctionCallContent{
			Arguments: "sample args",
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
			Data:      []byte("sample data"),
			Name:      "sample data name",
			URI:       "sample data uri",
			MediaType: "sample media type",
		},
		&message.HostedFileContent{
			FileID: "file-123",
		},
		&message.HostedVectorStoreContent{
			VectorStoreID: "store-123",
		},
	}
	data, err := json.Marshal(contents)
	if err != nil {
		t.Error(err)
	}
	var decoded message.Contents
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Error(err)
	}
	if len(decoded) != len(contents) {
		t.Errorf("expected %d contents, got %d", len(contents), len(decoded))
	}
	for i, v := range contents {
		if !reflect.DeepEqual(v, decoded[i]) {
			t.Errorf("[%d]: expected content %v, got %v", i, v, decoded[i])
		}
	}
}
