// Copyright (c) Microsoft. All rights reserved.

package copilotprovider

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestSessionConfig_WithApprovalRequiredTool_InstallsAskPreToolUseHook(t *testing.T) {
	dangerousTool := tool.ApprovalRequiredFunc(testFuncTool(t, "dangerous"))
	plainTool := testFuncTool(t, "plain")
	p := &provider{}

	cfg := p.sessionConfig(true, []agent.Option{
		agent.WithTool(dangerousTool),
		agent.WithTool(plainTool),
	})

	if cfg.Hooks == nil || cfg.Hooks.OnPreToolUse == nil {
		t.Fatal("OnPreToolUse hook was not installed")
	}

	dangerousDecision, err := cfg.Hooks.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "dangerous"}, copilot.HookInvocation{})
	if err != nil {
		t.Fatalf("OnPreToolUse(dangerous): %v", err)
	}
	if dangerousDecision == nil || dangerousDecision.PermissionDecision != "ask" {
		t.Fatalf("dangerous permission decision = %#v, want ask", dangerousDecision)
	}

	plainDecision, err := cfg.Hooks.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "plain"}, copilot.HookInvocation{})
	if err != nil {
		t.Fatalf("OnPreToolUse(plain): %v", err)
	}
	if plainDecision != nil {
		t.Fatalf("plain permission decision = %#v, want nil", plainDecision)
	}
}

func TestSessionConfig_RawSessionConfigToolNotGatedButFrameworkApprovalToolIs(t *testing.T) {
	// A raw copilot.Tool supplied via SessionConfig.Tools carries no approval
	// marker, so it must not be auto-gated. Only tools explicitly marked
	// approval-required via tool.ApprovalRequiredFunc are gated, mirroring .NET's
	// ApprovalRequiredAIFunction (SkipPermission is a separate, orthogonal concept).
	source := &copilot.SessionConfig{
		Tools: []copilot.Tool{{Name: "dangerous"}},
		Hooks: &copilot.SessionHooks{},
	}
	p := &provider{cfg: AgentConfig{SessionConfig: source}}

	cfg := p.sessionConfig(true, []agent.Option{
		agent.WithTool(tool.ApprovalRequiredFunc(testFuncTool(t, "fw-dangerous"))),
	})

	if source.Hooks.OnPreToolUse != nil {
		t.Fatal("source hooks were mutated")
	}
	if len(source.Tools) != 1 {
		t.Fatalf("source tools length = %d, want 1", len(source.Tools))
	}
	if len(cfg.Tools) != 2 {
		t.Fatalf("session tools length = %d, want 2", len(cfg.Tools))
	}
	if cfg.Hooks == nil || cfg.Hooks == source.Hooks {
		t.Fatal("session hooks were not cloned")
	}

	// The explicitly-marked framework tool is gated.
	fw, err := cfg.Hooks.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "fw-dangerous"}, copilot.HookInvocation{})
	if err != nil {
		t.Fatalf("OnPreToolUse(fw-dangerous): %v", err)
	}
	if fw == nil || fw.PermissionDecision != "ask" {
		t.Fatalf("fw-dangerous permission decision = %#v, want ask", fw)
	}

	// The raw SessionConfig tool carries no approval marker and must not be gated.
	raw, err := cfg.Hooks.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "dangerous"}, copilot.HookInvocation{})
	if err != nil {
		t.Fatalf("OnPreToolUse(dangerous): %v", err)
	}
	if raw != nil {
		t.Fatalf("raw session-config tool should not be gated, got %#v", raw)
	}
}

func TestSessionConfig_WithExistingPreToolUseHook_PreservesCallerHook(t *testing.T) {
	expected := &copilot.PreToolUseHookOutput{PermissionDecision: "allow"}
	source := &copilot.SessionConfig{
		Hooks: &copilot.SessionHooks{
			OnPreToolUse: func(copilot.PreToolUseHookInput, copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
				return expected, nil
			},
		},
	}
	p := &provider{cfg: AgentConfig{SessionConfig: source}}

	cfg := p.sessionConfig(true, []agent.Option{agent.WithTool(tool.ApprovalRequiredFunc(testFuncTool(t, "dangerous")))})

	if cfg.Hooks == nil || cfg.Hooks.OnPreToolUse == nil {
		t.Fatal("OnPreToolUse hook is nil")
	}
	got, err := cfg.Hooks.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "dangerous"}, copilot.HookInvocation{})
	if err != nil {
		t.Fatalf("OnPreToolUse: %v", err)
	}
	if got != expected {
		t.Fatalf("permission decision = %#v, want %#v", got, expected)
	}
}

func TestSessionConfig_WithExistingPreToolUseHookAndApprovalTools_LogsWarning(t *testing.T) {
	var logs bytes.Buffer
	defaultLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() {
		slog.SetDefault(defaultLogger)
	})

	source := &copilot.SessionConfig{
		Hooks: &copilot.SessionHooks{
			OnPreToolUse: func(copilot.PreToolUseHookInput, copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
				return nil, nil
			},
		},
	}
	p := &provider{cfg: AgentConfig{SessionConfig: source}}

	_ = p.sessionConfig(true, []agent.Option{
		agent.WithTool(tool.ApprovalRequiredFunc(testFuncTool(t, "dangerous-a"))),
		agent.WithTool(tool.ApprovalRequiredFunc(testFuncTool(t, "dangerous-b"))),
	})

	logOutput := logs.String()
	if !strings.Contains(logOutput, "not be automatically gated") {
		t.Fatalf("expected warning log for skipped approval gating, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "approvalRequiredToolCount=2") {
		t.Fatalf("expected tool count in warning log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "dangerous-a, dangerous-b") {
		t.Fatalf("expected tool names in warning log, got %q", logOutput)
	}
}

func TestResumeSessionConfig_WithApprovalRequiredTool_InstallsAskPreToolUseHook(t *testing.T) {
	p := &provider{}

	cfg := p.resumeSessionConfig(true, []agent.Option{
		agent.WithTool(tool.ApprovalRequiredFunc(testFuncTool(t, "dangerous"))),
	})

	if cfg.Hooks == nil || cfg.Hooks.OnPreToolUse == nil {
		t.Fatal("OnPreToolUse hook was not installed")
	}
	decision, err := cfg.Hooks.OnPreToolUse(copilot.PreToolUseHookInput{ToolName: "dangerous"}, copilot.HookInvocation{})
	if err != nil {
		t.Fatalf("OnPreToolUse: %v", err)
	}
	if decision == nil || decision.PermissionDecision != "ask" {
		t.Fatalf("dangerous permission decision = %#v, want ask", decision)
	}
}

func testFuncTool(t *testing.T, name string) tool.FuncTool {
	t.Helper()
	return functool.MustNew(
		functool.Config{Name: name, Description: name + " description"},
		func(context.Context, struct{}) (string, error) { return "ok", nil },
	)
}
