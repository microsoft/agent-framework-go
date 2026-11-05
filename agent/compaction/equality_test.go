// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"errors"
	"strings"
	"testing"
	"time"

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
	prefix := contentMessage(message.RoleUser, &message.TextContent{Text: "prefix"})
	index := CreateMessageIndex([]*message.Message{prefix, originalLast}, nil)
	index.Groups[0].IsExcluded = true
	index.Groups[0].ExcludeReason = "preserved"

	index.Update([]*message.Message{prefix, replacementLast})
	return len(index.Groups) > 0 && index.Groups[0].IsExcluded && index.Groups[0].ExcludeReason == "preserved"
}

func contentMessage(role message.Role, contents ...message.Content) *message.Message {
	return &message.Message{Role: role, Contents: contents}
}

func requireMessageContentEqual(t *testing.T, left, right *message.Message, want bool) {
	t.Helper()
	if got := messageContentEqual(left, right); got != want {
		t.Fatalf("messageContentEqual(%#v, %#v) = %v, want %v", left, right, got, want)
	}
}

func mustDataContent(t *testing.T, data []byte, mediaType, name string) *message.DataContent {
	t.Helper()
	content, err := message.NewDataContent(data, mediaType)
	if err != nil {
		t.Fatal(err)
	}
	content.Name = name
	return content
}

func TestMessageContentEqual_BothNullReturnsTrue(t *testing.T) {
	requireMessageContentEqual(t, nil, nil, true)
}

func TestMessageContentEqual_LeftNullReturnsFalse(t *testing.T) {
	requireMessageContentEqual(t, nil, contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"}), false)
}

func TestMessageContentEqual_RightNullReturnsFalse(t *testing.T) {
	requireMessageContentEqual(t, contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"}), nil, false)
}

func TestMessageContentEqual_SameReferenceReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	requireMessageContentEqual(t, a, a, true)
}

func TestMessageContentEqual_MatchingMessageIdReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	a.ID, b.ID = "msg-1", "msg-1"
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_MatchingMessageIdSufficientDespiteDifferentContent(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleAssistant, &message.TextContent{Text: "Goodbye"})
	a.ID, b.ID = "msg-1", "msg-1"
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentMessageIdReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	a.ID, b.ID = "msg-1", "msg-2"
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_OnlyLeftHasMessageIdFallsThroughToContentComparison(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	a.ID = "msg-1"
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_OnlyRightHasMessageIdFallsThroughToContentComparison(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b.ID = "msg-1"
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentRoleReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleAssistant, &message.TextContent{Text: "Hello"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentAuthorNameReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	a.AuthorName, b.AuthorName = "Alice", "Bob"
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_BothNullAuthorNamesAreEqual(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_EqualTextContentReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello world"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello world"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentTextContentReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "Goodbye"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_TextContentIsCaseSensitive(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "hello"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_EqualTextReasoningContentReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.TextReasoningContent{Text: "thinking...", ProtectedData: "opaque"})
	b := contentMessage(message.RoleAssistant, &message.TextReasoningContent{Text: "thinking...", ProtectedData: "opaque"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentReasoningTextReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.TextReasoningContent{Text: "alpha"})
	b := contentMessage(message.RoleAssistant, &message.TextReasoningContent{Text: "beta"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentProtectedDataReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.TextReasoningContent{Text: "same", ProtectedData: "x"})
	b := contentMessage(message.RoleAssistant, &message.TextReasoningContent{Text: "same", ProtectedData: "y"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_EqualDataContentReturnsTrue(t *testing.T) {
	data := []byte("payload")
	a := contentMessage(message.RoleUser, mustDataContent(t, data, "application/octet-stream", "file.bin"))
	b := contentMessage(message.RoleUser, mustDataContent(t, data, "application/octet-stream", "file.bin"))
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentDataBytesReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, mustDataContent(t, []byte("aaa"), "text/plain", ""))
	b := contentMessage(message.RoleUser, mustDataContent(t, []byte("bbb"), "text/plain", ""))
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentMediaTypeReturnsFalse(t *testing.T) {
	data := []byte{1, 2, 3}
	a := contentMessage(message.RoleUser, mustDataContent(t, data, "image/png", ""))
	b := contentMessage(message.RoleUser, mustDataContent(t, data, "image/jpeg", ""))
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentDataContentNameReturnsFalse(t *testing.T) {
	data := []byte{1, 2, 3}
	a := contentMessage(message.RoleUser, mustDataContent(t, data, "image/png", "a.png"))
	b := contentMessage(message.RoleUser, mustDataContent(t, data, "image/png", "b.png"))
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_EqualUriContentReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.URIContent{URI: "https://example.com/image.png", MediaType: "image/png"})
	b := contentMessage(message.RoleUser, &message.URIContent{URI: "https://example.com/image.png", MediaType: "image/png"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentUriReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.URIContent{URI: "https://a.com/x", MediaType: "image/png"})
	b := contentMessage(message.RoleUser, &message.URIContent{URI: "https://b.com/x", MediaType: "image/png"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentUriMediaTypeReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.URIContent{URI: "https://example.com/file", MediaType: "image/png"})
	b := contentMessage(message.RoleUser, &message.URIContent{URI: "https://example.com/file", MediaType: "image/jpeg"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_EqualErrorContentReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.ErrorContent{Message: "fail", ErrorCode: "E001"})
	b := contentMessage(message.RoleAssistant, &message.ErrorContent{Message: "fail", ErrorCode: "E001"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentErrorMessageReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.ErrorContent{Message: "fail"})
	b := contentMessage(message.RoleAssistant, &message.ErrorContent{Message: "crash"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentErrorCodeReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.ErrorContent{Message: "fail", ErrorCode: "E001"})
	b := contentMessage(message.RoleAssistant, &message.ErrorContent{Message: "fail", ErrorCode: "E002"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_EqualFunctionCallContentReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "get_weather", Arguments: `{"city":"Seattle"}`})
	b := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "get_weather", Arguments: `{"city":"Seattle"}`})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentCallIdReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "get_weather"})
	b := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-2", Name: "get_weather"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentFunctionNameReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "get_weather"})
	b := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "get_time"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentArgumentsReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "fn", Arguments: `{"x":"1"}`})
	b := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "fn", Arguments: `{"x":"2"}`})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_NullArgumentsBothSidesReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "fn"})
	b := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "fn"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_OneNullArgumentsReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "fn"})
	b := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "fn", Arguments: `{"x":"1"}`})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentArgumentCountReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "fn", Arguments: `{"x":"1"}`})
	b := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "call-1", Name: "fn", Arguments: `{"x":"1","y":"2"}`})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_EqualFunctionResultContentReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleTool, &message.FunctionResultContent{CallID: "call-1", Result: "sunny"})
	b := contentMessage(message.RoleTool, &message.FunctionResultContent{CallID: "call-1", Result: "sunny"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentResultCallIdReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleTool, &message.FunctionResultContent{CallID: "call-1", Result: "sunny"})
	b := contentMessage(message.RoleTool, &message.FunctionResultContent{CallID: "call-2", Result: "sunny"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentResultValueReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleTool, &message.FunctionResultContent{CallID: "call-1", Result: "sunny"})
	b := contentMessage(message.RoleTool, &message.FunctionResultContent{CallID: "call-1", Result: "rainy"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_EqualHostedFileContentReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.HostedFileContent{FileID: "file-abc", MediaType: "text/csv", Name: "data.csv"})
	b := contentMessage(message.RoleUser, &message.HostedFileContent{FileID: "file-abc", MediaType: "text/csv", Name: "data.csv"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentFileIdReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.HostedFileContent{FileID: "file-abc"})
	b := contentMessage(message.RoleUser, &message.HostedFileContent{FileID: "file-xyz"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentHostedFileMediaTypeReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.HostedFileContent{FileID: "file-abc", MediaType: "text/csv"})
	b := contentMessage(message.RoleUser, &message.HostedFileContent{FileID: "file-abc", MediaType: "text/plain"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentHostedFileNameReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.HostedFileContent{FileID: "file-abc", Name: "a.csv"})
	b := contentMessage(message.RoleUser, &message.HostedFileContent{FileID: "file-abc", Name: "b.csv"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_DifferentContentCountReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &message.TextContent{Text: "one"}, &message.TextContent{Text: "two"})
	b := contentMessage(message.RoleUser, &message.TextContent{Text: "one"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_MixedContentTypesInSameOrderReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.TextContent{Text: "reply"}, &message.FunctionCallContent{CallID: "c1", Name: "fn"})
	b := contentMessage(message.RoleAssistant, &message.TextContent{Text: "reply"}, &message.FunctionCallContent{CallID: "c1", Name: "fn"})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_MismatchedContentTypeOrderReturnsFalse(t *testing.T) {
	a := contentMessage(message.RoleAssistant, &message.TextContent{Text: "reply"}, &message.FunctionCallContent{CallID: "c1", Name: "fn"})
	b := contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "c1", Name: "fn"}, &message.TextContent{Text: "reply"})
	requireMessageContentEqual(t, a, b, false)
}

func TestMessageContentEqual_EmptyContentsListsAreEqual(t *testing.T) {
	a := contentMessage(message.RoleUser)
	b := contentMessage(message.RoleUser)
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_SameContentItemReferenceReturnsTrue(t *testing.T) {
	shared := &message.TextContent{Text: "Hello"}
	a := contentMessage(message.RoleUser, shared)
	b := contentMessage(message.RoleUser, shared)
	requireMessageContentEqual(t, a, b, true)
}

type (
	unknownContent      struct{ message.Content }
	otherUnknownContent struct{ message.Content }
)

func TestMessageContentEqual_UnknownContentSubtypeSameTypeReturnsTrue(t *testing.T) {
	a := contentMessage(message.RoleUser, &unknownContent{})
	b := contentMessage(message.RoleUser, &unknownContent{})
	requireMessageContentEqual(t, a, b, true)
}

func TestMessageContentEqual_DifferentUnknownContentSubtypesReturnFalse(t *testing.T) {
	a := contentMessage(message.RoleUser, &unknownContent{})
	b := contentMessage(message.RoleUser, &otherUnknownContent{})
	requireMessageContentEqual(t, a, b, false)
}

type wordCounter struct{}

func (wordCounter) CountTokens(text string) int { return len(strings.Fields(text)) }

func TestMessageIndex_ComputeByteCountHandlesTextAndNonTextContent(t *testing.T) {
	messages := []*message.Message{
		contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"}),
		contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "c1", Name: "fn"}),
	}
	if got := computeByteCount(messages); got != 9 {
		t.Fatalf("computeByteCount = %d, want 9", got)
	}
}

func TestMessageIndex_ComputeByteCountTextContent(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})}
	if got := computeByteCount(messages); got != 5 {
		t.Fatalf("computeByteCount = %d, want 5", got)
	}
}

func TestMessageIndex_ComputeByteCountTextReasoningContent(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleAssistant, &message.TextReasoningContent{Text: "think", ProtectedData: "secret"})}
	if got := computeByteCount(messages); got != 11 {
		t.Fatalf("computeByteCount = %d, want 11", got)
	}
}

func TestMessageIndex_ComputeByteCountDataContent(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleUser, mustDataContent(t, make([]byte, 100), "image/png", "pic"))}
	if got := computeByteCount(messages); got != 112 {
		t.Fatalf("computeByteCount = %d, want 112", got)
	}
}

func TestMessageIndex_ComputeByteCountUriContent(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleUser, &message.URIContent{URI: "https://example.com/image.png", MediaType: "image/png"})}
	if got := computeByteCount(messages); got != 38 {
		t.Fatalf("computeByteCount = %d, want 38", got)
	}
}

func TestMessageIndex_ComputeByteCountFunctionCallContentWithArguments(t *testing.T) {
	const (
		callID       = "call1"
		functionName = "get_weather"
		arguments    = `{"city":"Seattle"}`
		wantBytes    = len(callID) + len(functionName) + len(arguments)
	)
	messages := []*message.Message{contentMessage(message.RoleAssistant, &message.FunctionCallContent{
		CallID: callID, Name: functionName, Arguments: arguments,
	})}
	if got := computeByteCount(messages); got != wantBytes {
		t.Fatalf("computeByteCount = %d, want %d", got, wantBytes)
	}
}

func TestMessageIndex_ComputeByteCountFunctionCallContentWithoutArguments(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "c1", Name: "fn"})}
	if got := computeByteCount(messages); got != 4 {
		t.Fatalf("computeByteCount = %d, want 4", got)
	}
}

func TestMessageIndex_ComputeByteCountFunctionResultContent(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleTool, &message.FunctionResultContent{CallID: "call1", Result: "Sunny, 72°F"})}
	if got, want := computeByteCount(messages), 5+len("Sunny, 72°F"); got != want {
		t.Fatalf("computeByteCount = %d, want %d", got, want)
	}
}

func TestMessageIndex_ComputeByteCountErrorContent(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleAssistant, &message.ErrorContent{Message: "fail", ErrorCode: "E001"})}
	if got := computeByteCount(messages); got != 8 {
		t.Fatalf("computeByteCount = %d, want 8", got)
	}
}

func TestMessageIndex_ComputeByteCountHostedFileContent(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleAssistant, &message.HostedFileContent{FileID: "file-abc", MediaType: "text/plain", Name: "readme.txt"})}
	if got := computeByteCount(messages); got != 28 {
		t.Fatalf("computeByteCount = %d, want 28", got)
	}
}

func TestMessageIndex_ComputeByteCountMixedContentInSingleMessage(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"}, mustDataContent(t, make([]byte, 50), "image/png", ""))}
	if got := computeByteCount(messages); got != 64 {
		t.Fatalf("computeByteCount = %d, want 64", got)
	}
}

func TestMessageIndex_ComputeByteCountEmptyContentsReturnsZero(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleUser)}
	if got := computeByteCount(messages); got != 0 {
		t.Fatalf("computeByteCount = %d, want 0", got)
	}
}

func TestMessageIndex_ComputeByteCountUnknownContentTypeReturnsZero(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleAssistant, &message.UsageContent{})}
	if got := computeByteCount(messages); got != 0 {
		t.Fatalf("computeByteCount = %d, want 0", got)
	}
}

func TestMessageIndex_ComputeTokenCountReturnsTokenCount(t *testing.T) {
	messages := []*message.Message{
		contentMessage(message.RoleUser, &message.TextContent{Text: "Hello world"}),
		contentMessage(message.RoleAssistant, &message.TextContent{Text: "Greetings"}),
	}
	if got := computeTokenCount(messages, wordCounter{}); got != 3 {
		t.Fatalf("computeTokenCount = %d, want 3", got)
	}
}

func TestMessageIndex_ComputeTokenCountEmptyContentsReturnsZero(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleUser)}
	if got := computeTokenCount(messages, wordCounter{}); got != 0 {
		t.Fatalf("computeTokenCount = %d, want 0", got)
	}
}

func TestMessageIndex_ComputeTokenCountHandlesTextAndNonTextContent(t *testing.T) {
	messages := []*message.Message{
		contentMessage(message.RoleUser, &message.TextContent{Text: "Hello world"}),
		contentMessage(message.RoleAssistant, &message.FunctionCallContent{CallID: "c1", Name: "fn"}),
	}
	if got := computeTokenCount(messages, wordCounter{}); got != 3 {
		t.Fatalf("computeTokenCount = %d, want 3", got)
	}
}

func TestMessageIndex_ComputeTokenCountTextReasoningContentUsesTokenizer(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleAssistant, &message.TextReasoningContent{Text: "deep thinking here", ProtectedData: "hidden data"})}
	if got := computeTokenCount(messages, wordCounter{}); got != 5 {
		t.Fatalf("computeTokenCount = %d, want 5", got)
	}
}

func TestMessageIndex_ComputeTokenCountNonTextContentEstimatesFromBytes(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleUser, mustDataContent(t, make([]byte, 40), "image/png", ""))}
	if got := computeTokenCount(messages, wordCounter{}); got != 12 {
		t.Fatalf("computeTokenCount = %d, want 12", got)
	}
}

func TestMessageIndex_ComputeTokenCountMixedTextAndNonTextContent(t *testing.T) {
	messages := []*message.Message{contentMessage(message.RoleUser, &message.TextContent{Text: "Hello world"}, mustDataContent(t, make([]byte, 40), "image/png", ""))}
	if got := computeTokenCount(messages, wordCounter{}); got != 14 {
		t.Fatalf("computeTokenCount = %d, want 14", got)
	}
}

func TestMessageIndex_MessageGroupStoresPassedCounts(t *testing.T) {
	group := newMessageGroup(GroupKindUser, []*message.Message{contentMessage(message.RoleUser, &message.TextContent{Text: "Hello"})}, 5, 2, nil)
	if group.MessageCount != 1 || group.ByteCount != 5 || group.TokenCount != 2 {
		t.Fatalf("group counts = (%d, %d, %d), want (1, 5, 2)", group.MessageCount, group.ByteCount, group.TokenCount)
	}
}

func TestCompactionProvider_DefaultStateKeyUsesStrategyTypeName(t *testing.T) {
	for _, tc := range []struct {
		strategy Strategy
		want     string
	}{
		{new(TruncationStrategy), "TruncationStrategy"},
		{new(SlidingWindowStrategy), "SlidingWindowStrategy"},
		{new(ToolResultStrategy), "ToolResultStrategy"},
		{new(SummarizationStrategy), "SummarizationStrategy"},
		{new(PipelineStrategy), "PipelineStrategy"},
		{new(ContextWindowStrategy), "ContextWindowStrategy"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			provider := NewContextProvider(ContextProviderConfig{Strategy: tc.strategy}).(*contextProvider)
			if provider.stateKey != tc.want {
				t.Fatalf("state key = %q, want %q", provider.stateKey, tc.want)
			}
			if provider.sourceID != defaultProviderSourceID {
				t.Fatalf("source ID changed to %q", provider.sourceID)
			}
		})
	}
}

func TestCompactionProvider_StateKeysAreStableAcrossEquivalentInstances(t *testing.T) {
	first := NewContextProvider(ContextProviderConfig{
		Strategy: &TruncationStrategy{Trigger: TokensExceed(100000)},
	}).(*contextProvider)
	second := NewContextProvider(ContextProviderConfig{
		Strategy: &TruncationStrategy{Trigger: TokensExceed(100000)},
	}).(*contextProvider)
	if first.stateKey != second.stateKey {
		t.Fatalf("equivalent providers have different state keys: %q and %q", first.stateKey, second.stateKey)
	}
}

func TestCompactionProvider_StateKeysReturnsCustomKeyWhenProvided(t *testing.T) {
	provider := NewContextProvider(ContextProviderConfig{
		Strategy: &TruncationStrategy{Trigger: TokensExceed(100000)}, StateKey: "my-custom-key", SourceID: "custom-source",
	}).(*contextProvider)
	if provider.stateKey != "my-custom-key" {
		t.Fatalf("state key = %q, want my-custom-key", provider.stateKey)
	}
}

func TestCompactionProvider_CustomSourceIDRemainsDefaultStateKey(t *testing.T) {
	provider := NewContextProvider(ContextProviderConfig{
		Strategy: new(TruncationStrategy), SourceID: "custom-source",
	}).(*contextProvider)
	if provider.stateKey != "custom-source" {
		t.Fatalf("state key = %q, want custom-source", provider.stateKey)
	}
}
