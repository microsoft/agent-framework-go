// Copyright (c) Microsoft. All rights reserved.

package tool

import (
	"context"
	"strings"

	"github.com/microsoft/agent-framework-go/internal/toolcontext"
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

// requiredPrefix marks a ToolMode created by RequireTool with a specific tool name.
const requiredPrefix = "required:"

// Mode returns the base tool mode represented by m.
// Modes created by RequireTool return ToolModeRequired.
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

// RequiredTool returns the specific tool name required by m.
// It returns false unless m was created by [RequireTool].
func (m ToolMode) RequiredTool() (string, bool) {
	if strings.HasPrefix(string(m), requiredPrefix) && m != ToolModeRequired {
		name := strings.TrimPrefix(string(m), requiredPrefix)
		if name != "" {
			return name, true
		}
	}
	return "", false
}

// RequireTool returns a ToolMode that requires the named tool to be used.
func RequireTool(name string) ToolMode {
	name = strings.TrimSpace(name)
	if name == "" {
		panic("tool: required tool name cannot be blank")
	}
	return ToolMode(requiredPrefix + name)
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
	// During automatic tool invocation, [InvocationFromContext] exposes the call ID.
	Call(ctx context.Context, args string) (any, error)
}

// Invocation identifies the logical function call being executed.
// It is a correlation record, not an authorization or cross-run idempotency key.
type Invocation struct {
	// CallID is the ID from the originating function call. It matches the ID on
	// the function result and is preserved when an approved call resumes.
	// An empty provider-supplied ID remains empty; no synthetic ID is assigned.
	CallID string
}

// InvocationFromContext returns the invocation identity supplied by the automatic
// tool-call middleware. Tool wrappers can combine it with their tool reference,
// Call arguments, and returned result or error to correlate lifecycle events.
// The arguments passed to Call remain raw JSON; this accessor does not normalize them.
//
// The identity is scoped to the invocation context, including contexts derived by
// wrappers, and is independent for parallel calls. A direct Call with an unrelated
// context has no invocation identity. Direct calls made with an existing invocation
// context inherit that identity; they do not create a new logical function call.
func InvocationFromContext(ctx context.Context) (Invocation, bool) {
	callID, ok := toolcontext.CallIDFromContext(ctx)
	return Invocation{CallID: callID}, ok
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

// ApprovalRequiredFunc wraps a tool so that it requires user approval before invocation.
// If the tool already reports that it requires approval, it is returned as-is;
// otherwise it is wrapped so that it does.
func ApprovalRequiredFunc(t FuncTool) FuncTool {
	if t == nil {
		return nil
	}
	if approval, ok := t.(ApprovalRequiredTool); ok && approval.ApprovalRequired() {
		return t
	}
	return approvalRequiredFunc{t}
}
