// Copyright (c) Microsoft. All rights reserved.

package agent

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
