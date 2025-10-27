// Copyright (c) Microsoft. All rights reserved.

package message

import (
	"github.com/microsoft/agent-framework/golang/pkg/types"
)

// Content represents message content.
type Content interface {
	// ContentType returns the type of content.
	ContentType() string
}

// TextContent represents plain text content.
type TextContent struct {
	Text string
}

func (t *TextContent) ContentType() string { return "text" }

// DataContent represents structured data content.
type DataContent struct {
	Data      interface{}
	MediaType string
}

func (d *DataContent) ContentType() string { return "data" }

// URIContent represents content referenced by a URI.
type URIContent struct {
	URI       string
	MediaType string
}

func (u *URIContent) ContentType() string { return "uri" }

// ImageContent represents image content.
type ImageContent struct {
	URI    string
	Detail string // "auto", "low", "high"
}

func (i *ImageContent) ContentType() string { return "image" }

// AudioContent represents audio content.
type AudioContent struct {
	URI    string
	Format string
}

func (a *AudioContent) ContentType() string { return "audio" }

// FunctionCallContent represents a request to call a function/tool.
type FunctionCallContent struct {
	ID        string
	Name      string
	Arguments string // JSON-encoded arguments
}

func (f *FunctionCallContent) ContentType() string { return "function_call" }

// FunctionResultContent represents the result of a function/tool call.
type FunctionResultContent struct {
	CallID string
	Name   string
	Result string
}

func (f *FunctionResultContent) ContentType() string { return "function_result" }

// ErrorContent represents an error message.
type ErrorContent struct {
	Error string
	Code  string
}

func (e *ErrorContent) ContentType() string { return "error" }

// RefusalContent represents a refusal to respond.
type RefusalContent struct {
	Refusal string
}

func (r *RefusalContent) ContentType() string { return "refusal" }

// ThinkingContent represents the agent's internal reasoning.
type ThinkingContent struct {
	Thinking string
}

func (t *ThinkingContent) ContentType() string { return "thinking" }

// ChatMessage represents a message in a conversation.
type ChatMessage struct {
	Role     types.Role
	Contents []Content
	Name     string // Optional name of the message sender
}

// NewChatMessage creates a new ChatMessage with text content.
func NewChatMessage(role types.Role, text string) *ChatMessage {
	return &ChatMessage{
		Role:     role,
		Contents: []Content{&TextContent{Text: text}},
	}
}

// AddContent adds content to the message.
func (m *ChatMessage) AddContent(content Content) {
	m.Contents = append(m.Contents, content)
}

// ChatResponse represents a response from an agent or chat client.
type ChatResponse struct {
	Message      *ChatMessage
	FinishReason types.FinishReason
	Usage        *types.UsageDetails
	ModelID      string
}

// Text returns the first text content in the response, or empty string.
func (r *ChatResponse) Text() string {
	if r.Message == nil {
		return ""
	}
	for _, content := range r.Message.Contents {
		if textContent, ok := content.(*TextContent); ok {
			return textContent.Text
		}
	}
	return ""
}

// ChatResponseUpdate represents a streaming update from an agent or chat client.
type ChatResponseUpdate struct {
	Delta        *ChatMessage
	FinishReason types.FinishReason
	Usage        *types.UsageDetails
	ModelID      string
}
