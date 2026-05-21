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

// requiredPrefix marks a ToolMode created by RequireTools with specific tool names.
const requiredPrefix = "required:"

// Mode returns the base tool mode represented by m.
// Modes created by RequireTools return ToolModeRequired.
func (m ToolMode) Mode() ToolMode {
	switch m {
	case ToolModeAuto, ToolModeNone, ToolModeRequired:
		return m
	}
	if strings.HasPrefix(string(m), requiredPrefix) {
		return ToolModeRequired
	}
	return m
}

// Required returns the specific tool names required by m.
// It returns nil unless m was created by RequireTools.
func (m ToolMode) Required() []string {
	if strings.HasPrefix(string(m), requiredPrefix) && m != ToolModeRequired {
		return strings.Split(strings.TrimPrefix(string(m), requiredPrefix), ",")
	}
	return nil
}

// RequireTools returns a ToolMode that requires the named tools to be used.
func RequireTools(name ...string) ToolMode {
	return ToolMode(requiredPrefix + strings.Join(name, ","))
}

// Tool describes a tool that can be made available to an agent.
type Tool interface {
	// Name returns the provider-facing tool name.
	Name() string

	// Description returns the provider-facing tool description.
	Description() string
}

// SchemaTool describes a tool that exposes input and output schemas.
type SchemaTool interface {
	Tool

	// Schema returns the tool input schema.
	Schema() any

	// ReturnSchema returns the tool output schema.
	ReturnSchema() any
}

// FuncTool describes a schema-aware tool that can be invoked by an agent.
type FuncTool interface {
	SchemaTool

	// Call invokes the tool with raw JSON arguments.
	Call(ctx context.Context, args string) (any, error)
}

// ApprovalRequiredTool indicates whether a tool requires user approval before invocation.
type ApprovalRequiredTool interface {
	Tool

	// ApprovalRequired reports whether the tool requires user approval before invocation.
	ApprovalRequired() bool
}

// approvalRequiredFunc wraps a FuncTool and marks it as approval-required.
type approvalRequiredFunc struct {
	FuncTool
}

// ApprovalRequired reports that the wrapped tool requires user approval.
func (approvalRequiredFunc) ApprovalRequired() bool { return true }

// ApprovalRequiredFunc wraps a tool to indicate that it requires user approval before invocation.
// If the tool already requires approval, it is returned as-is.
// Not all tools support approval, in which case the original tool is returned.
func ApprovalRequiredFunc(t FuncTool) FuncTool {
	if approval, ok := t.(ApprovalRequiredTool); ok && approval.ApprovalRequired() {
		return t
	}
	return approvalRequiredFunc{t}
}
