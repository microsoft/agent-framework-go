// Copyright (c) Microsoft. All rights reserved.

package errors

import "fmt"

// AgentError is the base error type for all agent-related errors.
type AgentError struct {
	Message string
	Cause   error
}

func (e *AgentError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *AgentError) Unwrap() error {
	return e.Cause
}

// AgentExecutionError represents an error during agent execution.
type AgentExecutionError struct {
	AgentError
	AgentID string
}

// NewAgentExecutionError creates a new AgentExecutionError.
func NewAgentExecutionError(agentID, message string, cause error) *AgentExecutionError {
	return &AgentExecutionError{
		AgentError: AgentError{Message: message, Cause: cause},
		AgentID:    agentID,
	}
}

// AgentInitializationError represents an error during agent initialization.
type AgentInitializationError struct {
	AgentError
	AgentType string
}

// NewAgentInitializationError creates a new AgentInitializationError.
func NewAgentInitializationError(agentType, message string, cause error) *AgentInitializationError {
	return &AgentInitializationError{
		AgentError: AgentError{Message: message, Cause: cause},
		AgentType:  agentType,
	}
}

// ToolError represents an error during tool execution.
type ToolError struct {
	AgentError
	ToolName string
	ToolID   string
}

// NewToolError creates a new ToolError.
func NewToolError(toolName, toolID, message string, cause error) *ToolError {
	return &ToolError{
		AgentError: AgentError{Message: message, Cause: cause},
		ToolName:   toolName,
		ToolID:     toolID,
	}
}

// ContentError represents an error with message content.
type ContentError struct {
	AgentError
	ContentType string
}

// NewContentError creates a new ContentError.
func NewContentError(contentType, message string, cause error) *ContentError {
	return &ContentError{
		AgentError:  AgentError{Message: message, Cause: cause},
		ContentType: contentType,
	}
}

// WorkflowValidationError represents an error during workflow validation.
type WorkflowValidationError struct {
	AgentError
	WorkflowID string
}

// NewWorkflowValidationError creates a new WorkflowValidationError.
func NewWorkflowValidationError(workflowID, message string, cause error) *WorkflowValidationError {
	return &WorkflowValidationError{
		AgentError: AgentError{Message: message, Cause: cause},
		WorkflowID: workflowID,
	}
}

// ThreadError represents an error with thread operations.
type ThreadError struct {
	AgentError
	ThreadID string
}

// NewThreadError creates a new ThreadError.
func NewThreadError(threadID, message string, cause error) *ThreadError {
	return &ThreadError{
		AgentError: AgentError{Message: message, Cause: cause},
		ThreadID:   threadID,
	}
}
