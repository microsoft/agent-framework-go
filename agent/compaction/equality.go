// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"reflect"

	"github.com/microsoft/agent-framework-go/message"
)

func messageContentEqual(left, right *message.Message) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	if left.ID != "" && right.ID != "" {
		return left.ID == right.ID
	}
	if left.Role != right.Role || left.AuthorName != right.AuthorName {
		return false
	}
	return contentsEqual(left.Contents, right.Contents)
}

func contentsEqual(left, right []message.Content) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !contentEqual(left[i], right[i]) {
			return false
		}
	}
	return true
}

func contentEqual(left, right message.Content) bool {
	if left == right {
		return true
	}
	if reflect.TypeOf(left) != reflect.TypeOf(right) {
		return false
	}
	switch leftContent := left.(type) {
	case *message.TextContent:
		rightContent := right.(*message.TextContent)
		return leftContent.Text == rightContent.Text
	case *message.TextReasoningContent:
		rightContent := right.(*message.TextReasoningContent)
		return leftContent.Text == rightContent.Text && leftContent.ProtectedData == rightContent.ProtectedData
	case *message.DataContent:
		rightContent := right.(*message.DataContent)
		return leftContent.MediaType == rightContent.MediaType && leftContent.Name == rightContent.Name && leftContent.Data == rightContent.Data
	case *message.URIContent:
		rightContent := right.(*message.URIContent)
		return leftContent.URI == rightContent.URI && leftContent.MediaType == rightContent.MediaType
	case *message.ErrorContent:
		rightContent := right.(*message.ErrorContent)
		return leftContent.Message == rightContent.Message && leftContent.ErrorCode == rightContent.ErrorCode && leftContent.Details == rightContent.Details
	case *message.FunctionCallContent:
		rightContent := right.(*message.FunctionCallContent)
		return leftContent.CallID == rightContent.CallID && leftContent.Name == rightContent.Name && leftContent.Arguments == rightContent.Arguments &&
			errorsEqual(leftContent.Error, rightContent.Error) && leftContent.InformationalOnly == rightContent.InformationalOnly
	case *message.FunctionResultContent:
		rightContent := right.(*message.FunctionResultContent)
		return leftContent.CallID == rightContent.CallID && reflect.DeepEqual(leftContent.Result, rightContent.Result) &&
			errorsEqual(leftContent.Error, rightContent.Error)
	case *message.HostedFileContent:
		rightContent := right.(*message.HostedFileContent)
		return leftContent.FileID == rightContent.FileID && leftContent.MediaType == rightContent.MediaType && leftContent.Name == rightContent.Name
	default:
		return true
	}
}

// errorsEqual reports whether two content error values are equal. Errors are
// compared by their message string, matching how function content serializes
// its Error field to and from JSON.
func errorsEqual(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Error() == right.Error()
}
