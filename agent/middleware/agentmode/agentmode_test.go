// Copyright (c) Microsoft. All rights reserved.

package agentmode_test

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/middleware/agentmode"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func TestAgentModeProvider_InjectsTools(t *testing.T) {
	p := agentmode.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}

	outMessages, outOpts, err := p.ContextProvider().BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	if len(outMessages) < len(messages)+1 {
		t.Fatalf("expected at least %d messages, got %d", len(messages)+1, len(outMessages))
	}

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
	p := agentmode.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}

	outMessages, _, err := p.ContextProvider().BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	firstText := outMessages[0].Contents.Text()
	if firstText == "" {
		t.Fatal("expected instruction message")
	}
	if !strings.Contains(firstText, "plan") {
		t.Error("expected instructions to mention 'plan' as current mode")
	}
}

func TestAgentModeProvider_CustomModes(t *testing.T) {
	p := agentmode.New(&agentmode.Options{
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

	outMessages, _, err := p.ContextProvider().BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	firstText := outMessages[0].Contents.Text()
	if !strings.Contains(firstText, "draft") {
		t.Error("expected instructions to mention 'draft'")
	}
	if !strings.Contains(firstText, "review") {
		t.Error("expected instructions to mention 'review'")
	}
}

func TestAgentModeProvider_GetSetMode(t *testing.T) {
	p := agentmode.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	// Before any invocation, GetMode should return the default.
	mode := p.GetMode(opts...)
	if mode != "plan" {
		t.Errorf("expected default mode 'plan' before init, got %q", mode)
	}

	// Initialize state.
	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}
	_, _, _ = p.ContextProvider().BeforeRun(context.Background(), messages, opts...)

	mode = p.GetMode(opts...)
	if mode != "plan" {
		t.Errorf("expected mode 'plan', got %q", mode)
	}

	if err := p.SetMode("execute", opts...); err != nil {
		t.Fatal(err)
	}
	mode = p.GetMode(opts...)
	if mode != "execute" {
		t.Errorf("expected mode 'execute', got %q", mode)
	}
}

func TestAgentModeProvider_SetModeValidates(t *testing.T) {
	p := agentmode.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	err := p.SetMode("nonexistent", opts...)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("expected 'invalid mode' in error, got: %v", err)
	}
}

func TestAgentModeProvider_SetModeRecordsPreviousMode(t *testing.T) {
	p := agentmode.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}

	// Initialize state with default mode.
	_, _, err := p.ContextProvider().BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	// Change mode externally.
	if err := p.SetMode("execute", opts...); err != nil {
		t.Fatal(err)
	}

	// Next provide should inject a mode-change notification.
	outMessages, _, err := p.ContextProvider().BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	var foundNotification bool
	for _, msg := range outMessages {
		text := msg.Contents.Text()
		if strings.Contains(text, "Mode changed") {
			foundNotification = true
			break
		}
	}
	if !foundNotification {
		t.Error("expected mode-change notification message after SetMode")
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

func TestAgentModeProvider_DuplicateModeNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for duplicate mode name")
		}
	}()

	agentmode.New(&agentmode.Options{
		Modes: []agentmode.Mode{
			{Name: "plan", Description: "Plan mode"},
			{Name: "plan", Description: "Duplicate"},
		},
	})
}

func TestAgentModeProvider_EmptyModeNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty mode name")
		}
	}()

	agentmode.New(&agentmode.Options{
		Modes: []agentmode.Mode{
			{Name: "", Description: "No name"},
		},
	})
}
