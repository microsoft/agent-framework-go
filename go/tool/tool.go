// Copyright (c) Microsoft. All rights reserved.

package tool

import (
	"context"
)

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

type FuncTool interface {
	Tool

	Schema() any
	ReturnSchema() any

	Call(ctx context.Context, args map[string]any) (any, error)
}

type InitTool interface {
	Tool

	// Init performs any initialization required for the tool.
	Init(ctx context.Context) error
}

type LoaderTool interface {
	Tool

	LoadTools(ctx context.Context) ([]Tool, error)
}

// ApprovalRequiredTool indicates that a tool requires user approval before invocation.
type ApprovalRequiredTool interface {
	Tool

	ApprovalRequired() bool
}

type approvalRequiredFunc struct {
	FuncTool
}

func (approvalRequiredFunc) ApprovalRequired() bool {
	return true
}

// ApprovalRequiredFunc wraps a tool to indicate that it requires user approval before invocation.
// If the tool already requires approval, it is returned as-is.
// Not all tools support approval, in which case the original tool is returned.
func ApprovalRequiredFunc(t FuncTool) FuncTool {
	return approvalRequiredFunc{t}
}
