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

// Tool represents a tool or function that an agent can use.
type Tool struct {
	// ID returns the unique identifier.
	ID string

	// Name returns the name.
	Name string

	// Description returns a description of what the tool does.
	Description string

	// Schema returns the JSON schema for the tool's parameters.
	Schema map[string]any

	// Func is the function to execute the tool.
	Func func(ctx context.Context, arguments string) (string, error)
}
