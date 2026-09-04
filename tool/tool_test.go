// Copyright (c) Microsoft. All rights reserved.

package tool_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/tool"
)

func TestToolModeRequiredHasNoSpecificTool(t *testing.T) {
	if name, ok := tool.ToolModeRequired.RequiredTool(); ok {
		t.Fatalf("RequiredTool() = %q, true; want no specific tool", name)
	}
}

func TestRequireTool(t *testing.T) {
	mode := tool.RequireTool(" get_weather ")
	if string(mode) != "required:get_weather" {
		t.Fatalf("RequireTool() = %q, want %q", mode, "required:get_weather")
	}
	if mode.Mode() != tool.ToolModeRequired {
		t.Fatalf("Mode() = %q, want %q", mode.Mode(), tool.ToolModeRequired)
	}
	if name, ok := mode.RequiredTool(); !ok || name != "get_weather" {
		t.Fatalf("RequiredTool() = %q, %t; want %q, true", name, ok, "get_weather")
	}
}

func TestRequireToolRejectsBlankName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RequireTool did not panic")
		}
	}()
	tool.RequireTool(" ")
}

func TestToolModeRequired_EmptyEncodedNameHasNoSpecificTool(t *testing.T) {
	if name, ok := tool.ToolMode("required:").RequiredTool(); ok {
		t.Fatalf("RequiredTool() = %q, true; want no specific tool", name)
	}
}

func TestApprovalRequiredFunc_NilRemainsNil(t *testing.T) {
	var input tool.FuncTool
	if got := tool.ApprovalRequiredFunc(input); got != nil {
		t.Fatalf("ApprovalRequiredFunc(nil) = %T, want nil", got)
	}
}
