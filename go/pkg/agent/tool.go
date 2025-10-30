// Copyright (c) Microsoft. All rights reserved.

package agent

import "context"

// ToolMode represents how tools should be used by the agent.
type ToolMode string

const (
	// ToolModeAuto allows the agent to decide when to use tools.
	ToolModeAuto ToolMode = "auto"
	// ToolModeRequired forces the agent to use at least one tool.
	ToolModeRequired ToolMode = "required"
	// ToolModeNone disables tool usage.
	ToolModeNone ToolMode = "none"
)

type Tool interface {
	ToolInfo() (name string, description string)
}

type CallTool interface {
	Tool

	Schema() map[string]any
	Call(ctx context.Context, args map[string]any) (any, error)
}

var _ Tool = (*HostedWebSearchTool)(nil)

// HostedWebSearchTool represents a hosted tool that can be specified to an
// AI service to enable it to perform web searches.
//
// This tool does not itself implement web searches. It is a marker that can
// be used to inform a service that the service is allowed to perform web
// searches if the service is capable of doing so.
type HostedWebSearchTool struct {
	Description          string
	AdditionalProperties map[string]any
}

func (t *HostedWebSearchTool) ToolInfo() (name string, description string) {
	return "web_search", t.Description
}

func (t *HostedWebSearchTool) Properties() map[string]any {
	return t.AdditionalProperties
}
