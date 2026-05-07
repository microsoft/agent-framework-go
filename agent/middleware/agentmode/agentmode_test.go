// Copyright (c) Microsoft. All rights reserved.

package agentmode_test

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/middleware/agentmode"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func TestAgentModeProvider_InjectsTools(t *testing.T) {
	provider := agentmode.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}

	outMessages, outOpts, err := provider.BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	// Should have injected an instruction message.
	if len(outMessages) != len(messages)+1 {
		t.Fatalf("expected %d messages, got %d", len(messages)+1, len(outMessages))
	}

	// Check that tools were added.
	var tools []tool.Tool
	for _, opt := range outOpts {
		if tt, ok := opt.Value().(tool.Tool); ok {
			tools = append(tools, tt)
		}
	}
	expectedTools := map[string]bool{
		"AgentMode_Set": false,
		"AgentMode_Get": false,
	}
	for _, tt := range tools {
		if _, ok := expectedTools[tt.Name()]; ok {
			expectedTools[tt.Name()] = true
		}
	}
	for name, found := range expectedTools {
		if !found {
			t.Errorf("expected tool %q not found", name)
		}
	}
}

func TestAgentModeProvider_DefaultModePlan(t *testing.T) {
	provider := agentmode.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}

	outMessages, _, err := provider.BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	firstText := outMessages[0].Contents.Text()
	if firstText == "" {
		t.Fatal("expected instruction message")
	}
	// Default mode should be "plan".
	if !contains(firstText, "plan") {
		t.Error("expected instructions to mention 'plan' as current mode")
	}
}

func TestAgentModeProvider_CustomModes(t *testing.T) {
	provider := agentmode.New(&agentmode.Options{
		Modes: []agentmode.Mode{
			{Name: "draft", Description: "Draft mode"},
			{Name: "review", Description: "Review mode"},
		},
		DefaultMode: "draft",
	})
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}

	outMessages, _, err := provider.BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	firstText := outMessages[0].Contents.Text()
	if !contains(firstText, "draft") {
		t.Error("expected instructions to mention 'draft'")
	}
	if !contains(firstText, "review") {
		t.Error("expected instructions to mention 'review'")
	}
}

func TestAgentModeProvider_GetSetMode(t *testing.T) {
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	mode := agentmode.GetMode(opts...)
	if mode != "" {
		t.Errorf("expected empty mode before initialization, got %q", mode)
	}

	agentmode.SetMode("execute", opts...)
	mode = agentmode.GetMode(opts...)
	if mode != "execute" {
		t.Errorf("expected mode 'execute', got %q", mode)
	}
}

func TestAgentModeProvider_InvalidDefaultModePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid default mode")
		}
	}()

	agentmode.New(&agentmode.Options{
		Modes: []agentmode.Mode{
			{Name: "plan", Description: "Plan mode"},
		},
		DefaultMode: "nonexistent",
	})
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
