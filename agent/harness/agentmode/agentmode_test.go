// Copyright (c) Microsoft. All rights reserved.

package agentmode_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/agentmode"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func newMessages(text string) []*message.Message {
	return []*message.Message{message.NewText(text)}
}

func sessionOpts() []agent.Option {
	return []agent.Option{agent.WithSession(agenttest.CreateSession())}
}

func invokeProvider(provider *agentmode.Provider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

func collectTools(opts []agent.Option) []tool.Tool {
	var tools []tool.Tool
	for _, opt := range opts {
		if tt, ok := opt.Value().(tool.Tool); ok {
			tools = append(tools, tt)
		}
	}
	return tools
}

func collectInstructions(opts []agent.Option) string {
	var sb strings.Builder
	for inst := range agent.AllOptions(opts, agent.WithInstructions) {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(inst)
	}
	return sb.String()
}

// 1. ProvideAIContextAsync_ReturnsToolsAndInstructions
func TestProvide_ReturnsToolsAndInstructions(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	tools := collectTools(outOpts)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	instructions := collectInstructions(outOpts)
	if instructions == "" {
		t.Fatal("expected non-empty instructions")
	}
}

// 2. ProvideAIContextAsync_InstructionsIncludeCurrentMode
func TestProvide_InstructionsIncludeCurrentMode(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "plan") {
		t.Error("expected instructions to contain 'plan'")
	}
}

// 3. Options_CustomModes_AreUsed
func TestCustomModes_AreUsed(t *testing.T) {
	p := agentmode.New(agentmode.Config{
		Modes: []agentmode.Mode{
			{Name: "draft", Description: "Draft mode"},
			{Name: "review", Description: "Review mode"},
		},
	})
	opts := sessionOpts()

	mode := p.GetMode(opts...)
	if mode != "draft" {
		t.Errorf("expected default mode 'draft', got %q", mode)
	}
}

// 4. Options_CustomModes_SetModeValidatesAgainstList
func TestCustomModes_SetModeValidatesAgainstList(t *testing.T) {
	p := agentmode.New(agentmode.Config{
		Modes: []agentmode.Mode{
			{Name: "draft", Description: "Draft mode"},
			{Name: "review", Description: "Review mode"},
		},
	})
	opts := sessionOpts()

	if err := p.SetMode("review", opts...); err != nil {
		t.Fatalf("expected valid mode 'review' to succeed: %v", err)
	}
	if mode := p.GetMode(opts...); mode != "review" {
		t.Errorf("expected 'review', got %q", mode)
	}

	if err := p.SetMode("invalid", opts...); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

// 5. Options_CustomDefaultMode_IsUsed
func TestCustomDefaultMode_IsUsed(t *testing.T) {
	p := agentmode.New(agentmode.Config{
		Modes: []agentmode.Mode{
			{Name: "draft", Description: "Draft mode"},
			{Name: "review", Description: "Review mode"},
		},
		DefaultMode: "review",
	})
	opts := sessionOpts()

	mode := p.GetMode(opts...)
	if mode != "review" {
		t.Errorf("expected default mode 'review', got %q", mode)
	}
}

// 6. Options_InvalidDefaultMode_Throws
func TestInvalidDefaultMode_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid default mode")
		}
	}()
	agentmode.New(agentmode.Config{
		Modes: []agentmode.Mode{
			{Name: "plan", Description: "Plan mode"},
		},
		DefaultMode: "nonexistent",
	})
}

// 7. Options_EmptyModes_UsesDefaults
// In Go, an empty Modes slice is treated as "use defaults" (plan/execute).
func TestEmptyModes_UsesDefaults(t *testing.T) {
	p := agentmode.New(agentmode.Config{
		Modes: []agentmode.Mode{},
	})
	opts := sessionOpts()
	mode := p.GetMode(opts...)
	if mode != "plan" {
		t.Errorf("expected default mode 'plan' for empty modes, got %q", mode)
	}
}

// 8. Options_CustomModes_AppearInInstructions
func TestCustomModes_AppearInInstructions(t *testing.T) {
	p := agentmode.New(agentmode.Config{
		Modes: []agentmode.Mode{
			{Name: "alpha", Description: "Alpha mode description"},
			{Name: "beta", Description: "Beta mode description"},
		},
	})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "alpha") {
		t.Error("expected 'alpha' in instructions")
	}
	if !strings.Contains(instructions, "Alpha mode description") {
		t.Error("expected 'Alpha mode description' in instructions")
	}
	if !strings.Contains(instructions, "beta") {
		t.Error("expected 'beta' in instructions")
	}
	if !strings.Contains(instructions, "Beta mode description") {
		t.Error("expected 'Beta mode description' in instructions")
	}
}

// 9. AgentMode_RequiresNameAndDescription
func TestEmptyModeName_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty mode name")
		}
	}()
	agentmode.New(agentmode.Config{
		Modes: []agentmode.Mode{
			{Name: "", Description: "No name"},
		},
	})
}

// 10. Options_DuplicateModeNames_Throws
func TestDuplicateModeNames_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for duplicate mode names")
		}
	}()
	agentmode.New(agentmode.Config{
		Modes: []agentmode.Mode{
			{Name: "plan", Description: "Plan mode"},
			{Name: "plan", Description: "Duplicate"},
		},
	})
}

// 11. ExternalModeChange_InjectsNotificationMessage
func TestExternalModeChange_InjectsNotification(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()
	msgs := newMessages("hi")

	// Initialize state.
	_, _, err := invokeProvider(p, context.Background(), msgs, opts...)
	if err != nil {
		t.Fatal(err)
	}

	// Change mode externally.
	if err := p.SetMode("execute", opts...); err != nil {
		t.Fatal(err)
	}

	// Next provide should inject notification.
	outMessages, _, err := invokeProvider(p, context.Background(), msgs, opts...)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, msg := range outMessages {
		if strings.Contains(msg.Contents.Text(), "Mode changed") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected mode-change notification message")
	}
}

// 12. ExternalModeChange_NotificationClearedAfterFirstRead
func TestExternalModeChange_NotificationClearedAfterFirstRead(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()
	msgs := newMessages("hi")

	_, _, _ = invokeProvider(p, context.Background(), msgs, opts...)
	_ = p.SetMode("execute", opts...)

	// First read: should have notification.
	outMessages, _, _ := invokeProvider(p, context.Background(), msgs, opts...)
	hasNotification := false
	for _, msg := range outMessages {
		if strings.Contains(msg.Contents.Text(), "Mode changed") {
			hasNotification = true
			break
		}
	}
	if !hasNotification {
		t.Fatal("expected notification on first read")
	}

	// Second read: notification should be cleared.
	outMessages2, _, _ := invokeProvider(p, context.Background(), msgs, opts...)
	for _, msg := range outMessages2 {
		if strings.Contains(msg.Contents.Text(), "Mode changed") {
			t.Error("notification should have been cleared after first read")
		}
	}
}

// 13. ExternalModeChange_SameMode_NoNotification
func TestExternalModeChange_SameMode_NoNotification(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()
	msgs := newMessages("hi")

	_, _, _ = invokeProvider(p, context.Background(), msgs, opts...)

	// Set to same mode.
	_ = p.SetMode("plan", opts...)

	outMessages, _, _ := invokeProvider(p, context.Background(), msgs, opts...)
	for _, msg := range outMessages {
		if strings.Contains(msg.Contents.Text(), "Mode changed") {
			t.Error("should not inject notification when setting same mode")
		}
	}
}

// 14. SetMode_ChangesMode
func TestSetMode_ChangesMode(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_, _, _ = invokeProvider(p, context.Background(), newMessages("hi"), opts...)

	if err := p.SetMode("execute", opts...); err != nil {
		t.Fatal(err)
	}
	if mode := p.GetMode(opts...); mode != "execute" {
		t.Errorf("expected 'execute', got %q", mode)
	}
}

// 15. SetMode_ReturnsConfirmation — verified via instructions reflecting the new mode
func TestSetMode_ReflectedInInstructions(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_, _, _ = invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	_ = p.SetMode("execute", opts...)

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "execute") {
		t.Error("expected instructions to reflect 'execute' mode")
	}
}

// 16. SetMode_InvalidMode_Throws
func TestSetMode_InvalidMode_ReturnsError(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	err := p.SetMode("nonexistent", opts...)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("expected 'invalid mode' in error, got: %v", err)
	}
}

// 17. GetMode_ReturnsDefaultMode
func TestGetMode_ReturnsDefaultMode(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	mode := p.GetMode(opts...)
	if mode != "plan" {
		t.Errorf("expected 'plan', got %q", mode)
	}
}

// 18. GetMode_ReturnsUpdatedModeAfterSet
func TestGetMode_ReturnsUpdatedModeAfterSet(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_ = p.SetMode("execute", opts...)
	mode := p.GetMode(opts...)
	if mode != "execute" {
		t.Errorf("expected 'execute', got %q", mode)
	}
}

// 19. PublicGetMode_ReturnsDefaultMode
func TestPublicGetMode_ReturnsDefaultMode(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	if mode := p.GetMode(opts...); mode != "plan" {
		t.Errorf("expected 'plan', got %q", mode)
	}
}

// 20. PublicSetMode_ChangesMode
func TestPublicSetMode_ChangesMode(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	if err := p.SetMode("execute", opts...); err != nil {
		t.Fatal(err)
	}
	if mode := p.GetMode(opts...); mode != "execute" {
		t.Errorf("expected 'execute', got %q", mode)
	}
}

// 21. PublicSetMode_InvalidMode_Throws
func TestPublicSetMode_InvalidMode_ReturnsError(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	err := p.SetMode("bad", opts...)
	if err == nil {
		t.Fatal("expected error")
	}
}

// 22. PublicSetMode_ReflectedInToolResults
func TestPublicSetMode_ReflectedInInstructions(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_ = p.SetMode("execute", opts...)

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "execute") {
		t.Error("expected 'execute' in instructions after SetMode")
	}
}

// 23. State_PersistsAcrossInvocations
func TestState_PersistsAcrossInvocations(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_, _, _ = invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	_ = p.SetMode("execute", opts...)

	// Second invocation — mode should persist.
	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "execute") {
		t.Error("expected mode 'execute' to persist across invocations")
	}

	if mode := p.GetMode(opts...); mode != "execute" {
		t.Errorf("expected 'execute', got %q", mode)
	}
}

// 24. Options_CustomInstructions_OverridesDefault
func TestCustomInstructions_OverridesDefault(t *testing.T) {
	p := agentmode.New(agentmode.Config{
		Instructions: "Custom instructions for mode {current_mode}",
	})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "Custom instructions") {
		t.Error("expected custom instructions")
	}
	if !strings.Contains(instructions, "plan") {
		t.Error("expected {current_mode} to be replaced with 'plan'")
	}
}

// Verify tool names are present in options.
func TestToolNames(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	tools := collectTools(outOpts)
	names := make([]string, len(tools))
	for i, tt := range tools {
		names[i] = tt.Name()
	}

	if !slices.Contains(names, "mode_set") {
		t.Error("expected mode_set tool")
	}
	if !slices.Contains(names, "mode_get") {
		t.Error("expected mode_get tool")
	}
}

// Verify default instructions contain mode_set/mode_get tool references and mode-check guidance.
func TestDefaultInstructions_ContainToolNamesAndModeCheckGuidance(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	for _, want := range []string{"mode_get", "mode_set", "check the current mode", "Mandatory Mode based Workflow"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("expected instructions to contain %q", want)
		}
	}
}

// Verify default mode list uses section headers (#### mode_name) not bullet points.
func TestDefaultInstructions_ModesSectionHeaderFormat(t *testing.T) {
	p := agentmode.New(agentmode.Config{})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "#### plan") {
		t.Error("expected '#### plan' section header in instructions")
	}
	if !strings.Contains(instructions, "#### execute") {
		t.Error("expected '#### execute' section header in instructions")
	}
}
