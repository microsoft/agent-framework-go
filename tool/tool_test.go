// Copyright (c) Microsoft. All rights reserved.

package tool_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/tool"
)

func TestRequireTools_WithoutNamesUsesGenericRequiredMode(t *testing.T) {
	mode := tool.RequireTools()
	if mode != tool.ToolModeRequired {
		t.Fatalf("RequireTools() = %q, want %q", mode, tool.ToolModeRequired)
	}
	if names := mode.Required(); names != nil {
		t.Fatalf("Required() = %v, want nil", names)
	}
}

func TestToolModeRequired_EmptyEncodedNamesReturnsNil(t *testing.T) {
	if names := tool.ToolMode("required:").Required(); names != nil {
		t.Fatalf("Required() = %v, want nil", names)
	}
}

func TestApprovalRequiredFunc_NilRemainsNil(t *testing.T) {
	var input tool.FuncTool
	if got := tool.ApprovalRequiredFunc(input); got != nil {
		t.Fatalf("ApprovalRequiredFunc(nil) = %T, want nil", got)
	}
}
