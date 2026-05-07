// Copyright (c) Microsoft. All rights reserved.

package todo_test

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/middleware/todo"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func TestTodoProvider_InjectsTools(t *testing.T) {
	provider := todo.New(nil)
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

	// Check that tools were added to options.
	var tools []tool.Tool
	for _, opt := range outOpts {
		if tt, ok := opt.Value().(tool.Tool); ok {
			tools = append(tools, tt)
		}
	}
	expectedToolNames := map[string]bool{
		"TodoList_Add":          false,
		"TodoList_Complete":     false,
		"TodoList_Remove":       false,
		"TodoList_GetRemaining": false,
		"TodoList_GetAll":       false,
	}
	for _, tt := range tools {
		if _, ok := expectedToolNames[tt.Name()]; ok {
			expectedToolNames[tt.Name()] = true
		}
	}
	for name, found := range expectedToolNames {
		if !found {
			t.Errorf("expected tool %q not found in options", name)
		}
	}
}

func TestTodoProvider_InstructionContainsTodoList(t *testing.T) {
	provider := todo.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}

	outMessages, _, err := provider.BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

	// First message should contain the todo list instructions.
	firstText := outMessages[0].Contents.Text()
	if firstText == "" {
		t.Fatal("expected instruction message to have text content")
	}
	if !containsSubstring(firstText, "todo") {
		t.Error("expected instruction message to mention 'todo'")
	}
	if !containsSubstring(firstText, "none yet") {
		t.Error("expected empty todo list to show 'none yet'")
	}
}

func TestTodoProvider_CustomInstructions(t *testing.T) {
	provider := todo.New(&todo.Options{
		Instructions: "Custom instructions for todo",
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
	if !containsSubstring(firstText, "Custom instructions") {
		t.Error("expected custom instructions in first message")
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && contains(s, substr)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
