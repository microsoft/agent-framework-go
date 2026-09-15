// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"reflect"
	"slices"

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
		return textContentEqual(leftContent, right.(*message.TextContent))
	case *message.TextReasoningContent:
		return textReasoningContentEqual(leftContent, right.(*message.TextReasoningContent))
	case *message.DataContent:
		return dataContentEqual(leftContent, right.(*message.DataContent))
	case *message.URIContent:
		return uriContentEqual(leftContent, right.(*message.URIContent))
	case *message.ErrorContent:
		return errorContentEqual(leftContent, right.(*message.ErrorContent))
	case *message.FunctionCallContent:
		return functionCallContentEqual(leftContent, right.(*message.FunctionCallContent))
	case *message.FunctionResultContent:
		return functionResultContentEqual(leftContent, right.(*message.FunctionResultContent))
	case *message.HostedFileContent:
		return hostedFileContentEqual(leftContent, right.(*message.HostedFileContent))
	case *message.HostedVectorStoreContent:
		return leftContent.VectorStoreID == right.(*message.HostedVectorStoreContent).VectorStoreID
	case *message.MCPServerToolCallContent:
		rightContent := right.(*message.MCPServerToolCallContent)
		return leftContent.CallID == rightContent.CallID && leftContent.Name == rightContent.Name &&
			leftContent.ServerName == rightContent.ServerName && leftContent.Arguments == rightContent.Arguments
	case *message.MCPServerToolResultContent:
		rightContent := right.(*message.MCPServerToolResultContent)
		return leftContent.CallID == rightContent.CallID && contentsEqual(leftContent.Outputs, rightContent.Outputs)
	case *message.CodeInterpreterToolCallContent:
		rightContent := right.(*message.CodeInterpreterToolCallContent)
		return leftContent.CallID == rightContent.CallID && contentsEqual(leftContent.Inputs, rightContent.Inputs)
	case *message.CodeInterpreterToolResultContent:
		rightContent := right.(*message.CodeInterpreterToolResultContent)
		return leftContent.CallID == rightContent.CallID && contentsEqual(leftContent.Outputs, rightContent.Outputs)
	case *message.ImageGenerationToolCallContent:
		return leftContent.CallID == right.(*message.ImageGenerationToolCallContent).CallID
	case *message.ImageGenerationToolResultContent:
		rightContent := right.(*message.ImageGenerationToolResultContent)
		return leftContent.CallID == rightContent.CallID && contentsEqual(leftContent.Outputs, rightContent.Outputs)
	case *message.WebSearchToolCallContent:
		rightContent := right.(*message.WebSearchToolCallContent)
		return leftContent.CallID == rightContent.CallID && slices.Equal(leftContent.Queries, rightContent.Queries)
	case *message.WebSearchToolResultContent:
		rightContent := right.(*message.WebSearchToolResultContent)
		return leftContent.CallID == rightContent.CallID && contentsEqual(leftContent.Outputs, rightContent.Outputs)
	case *message.UsageContent:
		return reflect.DeepEqual(leftContent.Details, right.(*message.UsageContent).Details)
	case *message.ToolApprovalRequestContent:
		rightContent := right.(*message.ToolApprovalRequestContent)
		return leftContent.RequestID == rightContent.RequestID && contentEqual(leftContent.ToolCall, rightContent.ToolCall)
	case *message.ToolApprovalResponseContent:
		rightContent := right.(*message.ToolApprovalResponseContent)
		return leftContent.RequestID == rightContent.RequestID && leftContent.Approved == rightContent.Approved &&
			leftContent.Reason == rightContent.Reason && contentEqual(leftContent.ToolCall, rightContent.ToolCall)
	case *message.AlwaysApproveToolApprovalResponseContent:
		rightContent := right.(*message.AlwaysApproveToolApprovalResponseContent)
		return leftContent.AlwaysApproveTool == rightContent.AlwaysApproveTool &&
			leftContent.AlwaysApproveToolWithArguments == rightContent.AlwaysApproveToolWithArguments &&
			contentEqual(leftContent.InnerResponse, rightContent.InnerResponse)
	case *message.RawContent:
		return reflect.DeepEqual(leftContent.RawRepresentation, right.(*message.RawContent).RawRepresentation)
	default:
		return true
	}
}

func textContentEqual(left, right *message.TextContent) bool {
	return left.Text == right.Text
}

func textReasoningContentEqual(left, right *message.TextReasoningContent) bool {
	return left.Text == right.Text && left.ProtectedData == right.ProtectedData
}

func dataContentEqual(left, right *message.DataContent) bool {
	return left.MediaType == right.MediaType && left.Name == right.Name && left.Data == right.Data
}

func uriContentEqual(left, right *message.URIContent) bool {
	return left.URI == right.URI && left.MediaType == right.MediaType
}

func errorContentEqual(left, right *message.ErrorContent) bool {
	return left.Message == right.Message && left.ErrorCode == right.ErrorCode && left.Details == right.Details
}

func functionCallContentEqual(left, right *message.FunctionCallContent) bool {
	return left.CallID == right.CallID && left.Name == right.Name && left.Arguments == right.Arguments &&
		errorsEqual(left.Error, right.Error) && left.InformationalOnly == right.InformationalOnly
}

func functionResultContentEqual(left, right *message.FunctionResultContent) bool {
	return left.CallID == right.CallID && reflect.DeepEqual(left.Result, right.Result) &&
		errorsEqual(left.Error, right.Error)
}

func hostedFileContentEqual(left, right *message.HostedFileContent) bool {
	return left.FileID == right.FileID && left.MediaType == right.MediaType && left.Name == right.Name &&
		reflect.DeepEqual(left.SizeInBytes, right.SizeInBytes) && reflect.DeepEqual(left.CreatedAt, right.CreatedAt)
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
