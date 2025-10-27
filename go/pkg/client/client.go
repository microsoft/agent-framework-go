// Copyright (c) Microsoft. All rights reserved.

package client

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/pkg/message"
	"github.com/microsoft/agent-framework/go/pkg/tool"
	"github.com/microsoft/agent-framework/go/pkg/types"
)

// ChatClient represents a client for chat completions.
type ChatClient interface {
	// Complete generates a single response for the given messages.
	Complete(ctx context.Context, messages []*message.ChatMessage, options *ChatOptions) (*message.ChatResponse, error)

	// CompleteStream generates a streaming response for the given messages.
	CompleteStream(ctx context.Context, messages []*message.ChatMessage, options *ChatOptions) iter.Seq2[*message.ChatResponseUpdate, error]
}

// ChatOptions contains options for chat completion.
type ChatOptions struct {
	// Tools to make available to the model.
	Tools []tool.Tool

	// ToolMode specifies how tools should be used.
	ToolMode types.ToolMode

	// Temperature controls randomness (0.0 to 2.0).
	Temperature *float64

	// TopP controls nucleus sampling (0.0 to 1.0).
	TopP *float64

	// MaxTokens limits the response length.
	MaxTokens *int

	// AdditionalMetadata for provider-specific options.
	AdditionalMetadata map[string]interface{}
}

// BaseChatClient provides common functionality for ChatClient implementations.
type BaseChatClient struct {
	ModelID string
}

// NewBaseChatClient creates a new BaseChatClient.
func NewBaseChatClient(modelID string) *BaseChatClient {
	return &BaseChatClient{
		ModelID: modelID,
	}
}
