// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"testing"

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
		{name: "function result", original: &message.FunctionResultContent{CallID: "c1", Result: "sunny"}, replacement: &message.FunctionResultContent{CallID: "c1", Result: "rainy"}, matches: false},
		{name: "hosted file", original: &message.HostedFileContent{FileID: "file-1", MediaType: "text/csv", Name: "a.csv"}, replacement: &message.HostedFileContent{FileID: "file-1", MediaType: "text/csv", Name: "a.csv"}, matches: true},
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
