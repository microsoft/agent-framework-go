// Copyright (c) Microsoft. All rights reserved.

package tool

import (
	"context"
	"strings"
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

const requiredPrefix = "required:"

func (m ToolMode) Mode() ToolMode {
	if m == ToolModeAuto || m == ToolModeNone {
		return m
	}
	if strings.HasPrefix(string(m), requiredPrefix) {
		return ToolModeRequired
	}
	return m
}

func (m ToolMode) Required() []string {
	if m.Mode() == ToolModeRequired {
		return strings.Split(strings.TrimPrefix(string(m), requiredPrefix), ",")
	}
	return nil
}

func RequireTools(name ...string) ToolMode {
	return ToolMode(requiredPrefix + strings.Join(name, ","))
}

type Tool interface {
	Name() string
	Description() string
}

type FuncTool interface {
	Tool

	Schema() any
	ReturnSchema() any

	Call(ctx context.Context, args string) (any, error)
}

// ApprovalRequiredTool indicates that a tool requires user approval before invocation.
type ApprovalRequiredTool interface {
	Tool

	ApprovalRequired()
}

type approvalRequiredFunc struct {
	FuncTool
}

func (approvalRequiredFunc) ApprovalRequired() {}

// ApprovalRequiredFunc wraps a tool to indicate that it requires user approval before invocation.
// If the tool already requires approval, it is returned as-is.
// Not all tools support approval, in which case the original tool is returned.
func ApprovalRequiredFunc(t FuncTool) FuncTool {
	return approvalRequiredFunc{t}
}
