// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"errors"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/message"
)

func TestMessageIndexUpdate_MatchesExistingMessagesByID(t *testing.T) {
	original := &message.Message{ID: "msg-1", Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hello"}}}
	replacement := &message.Message{ID: "msg-1", Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "goodbye"}}}
	if !updatePreservesExistingGroups(original, replacement) {
		t.Fatal("expected matching IDs to preserve the existing index")
	}

	replacement.ID = "msg-2"
	if updatePreservesExistingGroups(original, replacement) {
		t.Fatal("expected different IDs to rebuild the index")
	}

	replacement.ID = ""
	replacement.Role = message.RoleUser
	replacement.Contents = []message.Content{&message.TextContent{Text: "hello"}}
	if !updatePreservesExistingGroups(original, replacement) {
		t.Fatal("expected content comparison when only one side has an ID")
	}
}

func TestMessageIndexUpdate_UsesRoleAuthorAndTextForMatching(t *testing.T) {
	original := &message.Message{Role: message.RoleUser, AuthorName: "Alice", Contents: []message.Content{&message.TextContent{Text: "Hello"}}}
	matching := &message.Message{Role: message.RoleUser, AuthorName: "Alice", Contents: []message.Content{&message.TextContent{Text: "Hello"}}}
	if !updatePreservesExistingGroups(original, matching) {
		t.Fatal("expected matching role, author, and text to preserve the existing index")
	}

	differentAuthor := matching.Clone()
	differentAuthor.AuthorName = "Bob"
	if updatePreservesExistingGroups(original, differentAuthor) {
		t.Fatal("expected different authors to rebuild the index")
	}

	differentText := matching.Clone()
	differentText.Contents = []message.Content{&message.TextContent{Text: "hello"}}
	if updatePreservesExistingGroups(original, differentText) {
		t.Fatal("expected text comparison to be case-sensitive")
	}
}

func TestMessageIndexUpdate_MatchesKnownContentTypes(t *testing.T) {
	tests := []struct {
		name        string
		original    message.Content
		replacement message.Content
		matches     bool
	}{
		{name: "reasoning", original: &message.TextReasoningContent{Text: "same", ProtectedData: "x"}, replacement: &message.TextReasoningContent{Text: "same", ProtectedData: "x"}, matches: true},
		{name: "reasoning protected data", original: &message.TextReasoningContent{Text: "same", ProtectedData: "x"}, replacement: &message.TextReasoningContent{Text: "same", ProtectedData: "y"}, matches: false},
		{name: "data", original: &message.DataContent{Data: "aaa", MediaType: "text/plain", Name: "a.txt"}, replacement: &message.DataContent{Data: "aaa", MediaType: "text/plain", Name: "a.txt"}, matches: true},
		{name: "uri", original: &message.URIContent{URI: "https://example.com/a", MediaType: "image/png"}, replacement: &message.URIContent{URI: "https://example.com/b", MediaType: "image/png"}, matches: false},
		{name: "error", original: &message.ErrorContent{Message: "fail", ErrorCode: "E1"}, replacement: &message.ErrorContent{Message: "fail", ErrorCode: "E2"}, matches: false},
		{name: "function call", original: &message.FunctionCallContent{CallID: "c1", Name: "fn", Arguments: `{"x":1}`}, replacement: &message.FunctionCallContent{CallID: "c1", Name: "fn", Arguments: `{"x":1}`}, matches: true},
		{name: "function call informational", original: &message.FunctionCallContent{CallID: "c1", Name: "fn", Arguments: `{"x":1}`}, replacement: &message.FunctionCallContent{CallID: "c1", Name: "fn", Arguments: `{"x":1}`, InformationalOnly: true}, matches: false},
		{name: "function call error", original: &message.FunctionCallContent{CallID: "c1", Name: "fn", Arguments: `{"x":1}`}, replacement: &message.FunctionCallContent{CallID: "c1", Name: "fn", Arguments: `{"x":1}`, Error: errors.New("bad call")}, matches: false},
		{name: "function result", original: &message.FunctionResultContent{CallID: "c1", Result: "sunny"}, replacement: &message.FunctionResultContent{CallID: "c1", Result: "rainy"}, matches: false},
		{name: "function result error", original: &message.FunctionResultContent{CallID: "c1", Result: "sunny"}, replacement: &message.FunctionResultContent{CallID: "c1", Result: "sunny", Error: errors.New("boom")}, matches: false},
		{name: "hosted file", original: &message.HostedFileContent{FileID: "file-1", MediaType: "text/csv", Name: "a.csv"}, replacement: &message.HostedFileContent{FileID: "file-1", MediaType: "text/csv", Name: "a.csv"}, matches: true},
		{name: "hosted file size", original: &message.HostedFileContent{FileID: "file-1", SizeInBytes: ptr(int64(1))}, replacement: &message.HostedFileContent{FileID: "file-1", SizeInBytes: ptr(int64(2))}, matches: false},
		{name: "hosted file creation", original: &message.HostedFileContent{FileID: "file-1", CreatedAt: ptr(time.Unix(1, 0))}, replacement: &message.HostedFileContent{FileID: "file-1", CreatedAt: ptr(time.Unix(2, 0))}, matches: false},
		{name: "vector store", original: &message.HostedVectorStoreContent{VectorStoreID: "store-1"}, replacement: &message.HostedVectorStoreContent{VectorStoreID: "store-2"}, matches: false},
		{name: "MCP call", original: &message.MCPServerToolCallContent{CallID: "c1", Name: "search", Arguments: `{}`}, replacement: &message.MCPServerToolCallContent{CallID: "c1", Name: "delete", Arguments: `{}`}, matches: false},
		{name: "MCP result", original: &message.MCPServerToolResultContent{CallID: "c1", Outputs: message.Contents{&message.TextContent{Text: "a"}}}, replacement: &message.MCPServerToolResultContent{CallID: "c1", Outputs: message.Contents{&message.TextContent{Text: "b"}}}, matches: false},
		{name: "code interpreter call", original: &message.CodeInterpreterToolCallContent{CallID: "c1", Inputs: message.Contents{&message.TextContent{Text: "a"}}}, replacement: &message.CodeInterpreterToolCallContent{CallID: "c1", Inputs: message.Contents{&message.TextContent{Text: "b"}}}, matches: false},
		{name: "code interpreter result", original: &message.CodeInterpreterToolResultContent{CallID: "c1", Outputs: message.Contents{&message.TextContent{Text: "a"}}}, replacement: &message.CodeInterpreterToolResultContent{CallID: "c1", Outputs: message.Contents{&message.TextContent{Text: "b"}}}, matches: false},
		{name: "image generation call", original: &message.ImageGenerationToolCallContent{CallID: "c1"}, replacement: &message.ImageGenerationToolCallContent{CallID: "c2"}, matches: false},
		{name: "image generation result", original: &message.ImageGenerationToolResultContent{CallID: "c1", Outputs: message.Contents{&message.TextContent{Text: "a"}}}, replacement: &message.ImageGenerationToolResultContent{CallID: "c1", Outputs: message.Contents{&message.TextContent{Text: "b"}}}, matches: false},
		{name: "web search call", original: &message.WebSearchToolCallContent{CallID: "c1", Queries: []string{"a"}}, replacement: &message.WebSearchToolCallContent{CallID: "c1", Queries: []string{"b"}}, matches: false},
		{name: "web search result", original: &message.WebSearchToolResultContent{CallID: "c1", Outputs: message.Contents{&message.URIContent{URI: "https://a"}}}, replacement: &message.WebSearchToolResultContent{CallID: "c1", Outputs: message.Contents{&message.URIContent{URI: "https://b"}}}, matches: false},
		{name: "usage", original: &message.UsageContent{Details: message.UsageDetails{TotalTokenCount: 1}}, replacement: &message.UsageContent{Details: message.UsageDetails{TotalTokenCount: 2}}, matches: false},
		{name: "approval request", original: &message.ToolApprovalRequestContent{RequestID: "r1", ToolCall: &message.FunctionCallContent{CallID: "c1"}}, replacement: &message.ToolApprovalRequestContent{RequestID: "r1", ToolCall: &message.FunctionCallContent{CallID: "c2"}}, matches: false},
		{name: "approval response", original: &message.ToolApprovalResponseContent{RequestID: "r1", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "c1"}}, replacement: &message.ToolApprovalResponseContent{RequestID: "r1", Approved: false, ToolCall: &message.FunctionCallContent{CallID: "c1"}}, matches: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := &message.Message{Role: message.RoleUser, Contents: []message.Content{tt.original}}
			replacement := &message.Message{Role: message.RoleUser, Contents: []message.Content{tt.replacement}}
			if got := updatePreservesExistingGroups(original, replacement); got != tt.matches {
				t.Fatalf("unexpected update behavior: got %v want %v", got, tt.matches)
			}
		})
	}
}

func ptr[T any](value T) *T { return &value }

func TestMessageIndexUpdate_UsesContentListStructureForMatching(t *testing.T) {
	original := &message.Message{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "reply"}, &message.FunctionCallContent{CallID: "c1", Name: "fn"}}}
	differentCount := &message.Message{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "reply"}}}
	if updatePreservesExistingGroups(original, differentCount) {
		t.Fatal("expected different content counts to rebuild the index")
	}

	differentOrder := &message.Message{Role: message.RoleAssistant, Contents: []message.Content{&message.FunctionCallContent{CallID: "c1", Name: "fn"}, &message.TextContent{Text: "reply"}}}
	if updatePreservesExistingGroups(original, differentOrder) {
		t.Fatal("expected mismatched content order to rebuild the index")
	}

	shared := &message.TextContent{Text: "shared"}
	left := &message.Message{Role: message.RoleUser, Contents: []message.Content{shared}}
	right := &message.Message{Role: message.RoleUser, Contents: []message.Content{shared}}
	if !updatePreservesExistingGroups(left, right) {
		t.Fatal("expected shared content reference to preserve the existing index")
	}
}

func updatePreservesExistingGroups(originalLast, replacementLast *message.Message) bool {
	prefix := textMessage(message.RoleUser, "prefix")
	index := compaction.CreateMessageIndex([]*message.Message{prefix, originalLast}, nil)
	index.Groups[0].IsExcluded = true
	index.Groups[0].ExcludeReason = "preserved"

	index.Update([]*message.Message{prefix, replacementLast})
	return len(index.Groups) > 0 && index.Groups[0].IsExcluded && index.Groups[0].ExcludeReason == "preserved"
}
