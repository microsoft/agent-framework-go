// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"

	"github.com/microsoft/agent-framework/golang/pkg/message"
	"github.com/microsoft/agent-framework/golang/pkg/thread"
	"github.com/microsoft/agent-framework/golang/pkg/tool"
	"github.com/microsoft/agent-framework/golang/pkg/types"
)

// Agent represents an AI agent that can process messages and generate responses.
type Agent interface {
	types.Identifiable
	types.Nameable

	// Run executes the agent with the given messages and options.
	Run(ctx context.Context, messages []*message.ChatMessage, thread thread.AgentThread, options *RunOptions) (*RunResponse, error)

	// RunStream executes the agent and streams responses.
	RunStream(ctx context.Context, messages []*message.ChatMessage, thread thread.AgentThread, options *RunOptions) (<-chan *RunResponseUpdate, error)

	// GetNewThread creates a new thread for this agent.
	GetNewThread() thread.AgentThread

	// DeserializeThread deserializes a thread from JSON.
	DeserializeThread(data []byte) (thread.AgentThread, error)
}

// RunOptions contains options for agent execution.
type RunOptions struct {
	// Tools to make available to the agent.
	Tools []tool.Tool

	// ToolMode specifies how tools should be used.
	ToolMode types.ToolMode

	// MaxTurns limits the number of agent turns.
	MaxTurns int

	// Temperature controls randomness in generation.
	Temperature *float64

	// TopP controls nucleus sampling.
	TopP *float64

	// MaxTokens limits the response length.
	MaxTokens *int

	// AdditionalMetadata for provider-specific options.
	AdditionalMetadata map[string]interface{}
}

// RunResponse represents the result of an agent execution.
type RunResponse struct {
	Message      *message.ChatMessage
	FinishReason types.FinishReason
	Usage        *types.UsageDetails
	ThreadID     string
	ModelID      string
}

// Text returns the first text content in the response, or empty string.
func (r *RunResponse) Text() string {
	if r.Message == nil {
		return ""
	}
	for _, content := range r.Message.Contents {
		if textContent, ok := content.(*message.TextContent); ok {
			return textContent.Text
		}
	}
	return ""
}

// RunResponseUpdate represents a streaming update from an agent execution.
type RunResponseUpdate struct {
	Delta        *message.ChatMessage
	FinishReason types.FinishReason
	Usage        *types.UsageDetails
	ThreadID     string
	ModelID      string
}
