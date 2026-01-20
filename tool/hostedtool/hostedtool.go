// Copyright (c) Microsoft. All rights reserved.

// Package hostedtool provides definitions for hosted tools.
//
// Hosted tools do not themselves implement functionality. They are markers that can
// be used to inform a service that the service is allowed to perform certain actions
// if the service is capable of doing so.
package hostedtool

import (
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

var _ tool.Tool = (*WebSearch)(nil)

// WebSearch represents a hosted tool that can be specified to an
// AI service to enable it to perform web searches.
type WebSearch struct {
	AdditionalProperties map[string]any
}

func (t *WebSearch) Name() string {
	return "web_search"
}

func (t *WebSearch) Description() string {
	return ""
}

var _ tool.Tool = (*FileSearch)(nil)

// FileSearch represents a hosted tool that can be specified to an AI service
// to enable it to perform file search operations.
type FileSearch struct {
	AdditionalProperties map[string]any

	Inputs             []message.Content
	MaximumResultCount int
}

func (t *FileSearch) Name() string {
	return "file_search"
}

func (t *FileSearch) Description() string {
	return ""
}

var _ tool.Tool = (*CodeInterpreter)(nil)

// CodeInterpreter represents a hosted tool that can be specified to an AI service
// to enable it to execute code it generates.
type CodeInterpreter struct {
	AdditionalProperties map[string]any

	Inputs []message.Content
}

func (t *CodeInterpreter) Name() string {
	return "code_interpreter"
}

func (t *CodeInterpreter) Description() string {
	return ""
}

type MCPServer struct {
	AdditionalProperties map[string]any

	ServerName        string
	ServerDescription string
	ServerAddress     string
	Authorization     string
	AllowedTools      []string
	Headers           map[string]string
}

func (t *MCPServer) Name() string {
	return "mcp"
}

func (t *MCPServer) Description() string {
	return ""
}
