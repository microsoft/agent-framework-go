// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/tool"
)

// WithTool(nil) yields an option whose Value() is a nil tool.Tool. Collecting
// options with AllOptions must skip it gracefully (as GetOption does) rather
// than panic — a panic here aborts tool collection in the auto-call middleware
// and in every provider.
func TestAllOptions_NilToolIsSkippedNotPanic(t *testing.T) {
	var count int
	for range agent.AllOptions([]agent.Option{agent.WithTool(nil)}, agent.WithTool) { // must not panic
		count++
	}
	if count != 0 {
		t.Fatalf("expected the nil tool option to be skipped, got %d yielded", count)
	}
}

func TestConfig_WithEmptyValues_PreservesOptionalValues(t *testing.T) {
	cfg := agent.Config{
		RunOptions: []agent.Option{agent.WithInstructions(""), agent.WithTool(nil)},
	}
	if cfg.Name != "" || cfg.Description != "" || cfg.ContextProviders != nil || cfg.HistoryProvider != nil {
		t.Fatalf("unexpected optional values: %#v", cfg)
	}
	if instructions, ok := agent.GetOption(cfg.RunOptions, agent.WithInstructions); !ok || instructions != "" {
		t.Fatalf("instructions = (%q, %v), want an explicit empty value", instructions, ok)
	}
	if configuredTool, ok := agent.GetOption(cfg.RunOptions, agent.WithTool); ok || configuredTool != nil {
		t.Fatalf("tools = (%v, %v), want nil", configuredTool, ok)
	}
}

func TestConfig_WithToolsOnly_PreservesTools(t *testing.T) {
	configuredTool := &stubTool{name: "test"}
	cfg := agent.Config{Tools: []tool.Tool{configuredTool}}
	if cfg.Name != "" || cfg.Description != "" {
		t.Fatalf("name/description = %q/%q, want empty", cfg.Name, cfg.Description)
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0] != configuredTool {
		t.Fatalf("tools = %#v, want the configured tool", cfg.Tools)
	}
}

func TestConfig_WithNameInstructionsDescriptionAndTools_PreservesValues(t *testing.T) {
	configuredTool := &stubTool{name: "test"}
	cfg := agent.Config{
		Name: "Test name", Description: "Test description",
		Tools:      []tool.Tool{configuredTool},
		RunOptions: []agent.Option{agent.WithInstructions("Test instructions")},
	}
	if cfg.Name != "Test name" || cfg.Description != "Test description" {
		t.Fatalf("name/description = %q/%q", cfg.Name, cfg.Description)
	}
	if instructions, ok := agent.GetOption(cfg.RunOptions, agent.WithInstructions); !ok || instructions != "Test instructions" {
		t.Fatalf("instructions = (%q, %v), want Test instructions", instructions, ok)
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0] != configuredTool {
		t.Fatalf("tools = %#v, want the configured tool", cfg.Tools)
	}
}

func TestConfig_WithNameAndDescriptionOnly_LeavesRunOptionsUnset(t *testing.T) {
	cfg := agent.Config{Name: "Test name", Description: "Test description"}
	if cfg.Name != "Test name" || cfg.Description != "Test description" {
		t.Fatalf("name/description = %q/%q", cfg.Name, cfg.Description)
	}
	if cfg.RunOptions != nil {
		t.Fatalf("run options = %#v, want nil", cfg.RunOptions)
	}
}
