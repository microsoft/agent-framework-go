// Copyright (c) Microsoft. All rights reserved.

package agent

// Role represents the role of a message sender in a conversation.
type Role string

const (
	// RoleUser represents a message from the user.
	RoleUser Role = "user"
	// RoleAssistant represents a message from the assistant.
	RoleAssistant Role = "assistant"
	// RoleSystem represents a system message.
	RoleSystem Role = "system"
	// RoleTool represents a message from a tool execution.
	RoleTool Role = "tool"
)

// FinishReason represents the reason why the agent stopped generating.
type FinishReason string

const (
	// FinishReasonStop indicates the agent stopped naturally.
	FinishReasonStop FinishReason = "stop"
	// FinishReasonLength indicates the agent stopped due to length limit.
	FinishReasonLength FinishReason = "length"
	// FinishReasonToolCalls indicates the agent stopped to execute tools.
	FinishReasonToolCalls FinishReason = "tool_calls"
	// FinishReasonContentFilter indicates content was filtered.
	FinishReasonContentFilter FinishReason = "content_filter"
	// FinishReasonError indicates an error occurred.
	FinishReasonError FinishReason = "error"
)

// UsageDetails provides usage details about a request/response.
type UsageDetails struct {
	AdditionalCounts map[string]int64
	InputTokenCount  int64
	OutputTokenCount int64
	TotalTokenCount  int64
}
