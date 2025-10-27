// Copyright (c) Microsoft. All rights reserved.

package chat

import "context"

// ChatMiddleware intercepts chat client requests and responses.
type ChatMiddleware interface {
	// OnChatRequest is called before a chat completion request.
	OnChatRequest(ctx context.Context, chatCtx *ChatContext) error

	// OnChatResponse is called after a chat completion response.
	OnChatResponse(ctx context.Context, chatCtx *ChatContext, response *Message) error

	// OnChatError is called when a chat completion fails.
	OnChatError(ctx context.Context, chatCtx *ChatContext, err error) error
}

// ChatContext contains context for a chat completion.
type ChatContext struct {
	ModelID  string
	Messages []*Message
	Metadata map[string]any
}
