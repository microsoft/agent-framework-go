// Copyright (c) Microsoft. All rights reserved.

package client

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/pkg/message"
	"github.com/microsoft/agent-framework/go/pkg/tool"
)

// ChatClient represents a client for chat completions.
type ChatClient interface {
	// Complete generates a single response for the given messages.
	Complete(ctx context.Context, options *ChatOptions, messages ...*message.ChatMessage) (*message.ChatResponse, error)
}

// StreamableChatClient is the interface implemented by agents that support streaming responses.
type StreamableChatClient interface {
	ChatClient

	// CompleteStream generates a streaming response for the given messages.
	CompleteStream(ctx context.Context, options *ChatOptions, messages ...*message.ChatMessage) iter.Seq2[*message.ChatResponseUpdate, error]
}

// CompleteStream is a helper function to run an agent in streaming mode.
// If the agent does not implement [StreamableChatClient], it falls back to calling [ChatClient.Complete] sequentially.
func CompleteStream(ctx context.Context, client ChatClient, options *ChatOptions, messages ...*message.ChatMessage) iter.Seq2[*message.ChatResponseUpdate, error] {
	if agent, ok := client.(StreamableChatClient); ok {
		return agent.CompleteStream(ctx, options, messages...)
	}
	return func(yield func(*message.ChatResponseUpdate, error) bool) {
		resp, err := client.Complete(ctx, options, messages...)
		var runResp *message.ChatResponseUpdate
		if resp != nil {
			runResp = &message.ChatResponseUpdate{
				Delta:        resp.Message,
				FinishReason: resp.FinishReason,
				Usage:        resp.Usage,
				ModelID:      resp.ModelID,
			}
		}
		if !yield(runResp, err) {
			return
		}
	}
}

// ChatOptions contains options for chat completion.
type ChatOptions struct {
	// Tools to make available to the model.
	Tools []tool.Tool

	// ToolMode specifies how tools should be used.
	ToolMode tool.Mode

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
