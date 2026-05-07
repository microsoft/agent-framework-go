// Copyright (c) Microsoft. All rights reserved.

package todo_test

import (
	"context"
	"strings"
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

	_, outOpts, err := provider.BeforeRun(context.Background(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}

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

func TestTodoProvider_InjectsInstructionsAndTodoList(t *testing.T) {
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

	if len(outMessages) < 3 {
		t.Fatalf("expected at least 3 messages (instruction + todo list + original), got %d", len(outMessages))
	}

	firstText := outMessages[0].Contents.Text()
	if !strings.Contains(firstText, "Todo") {
		t.Error("expected instruction message to mention 'Todo'")
	}

	secondText := outMessages[1].Contents.Text()
	if !strings.Contains(secondText, "none yet") {
		t.Error("expected empty todo list to show 'none yet'")
	}
}

func TestTodoProvider_SuppressTodoListMessage(t *testing.T) {
	provider := todo.New(&todo.Options{
		SuppressTodoListMessage: true,
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

	if len(outMessages) != len(messages)+1 {
		t.Fatalf("expected %d messages with suppressed todo, got %d", len(messages)+1, len(outMessages))
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
	if !strings.Contains(firstText, "Custom instructions") {
		t.Error("expected custom instructions in first message")
	}
}

func TestTodoProvider_CustomTodoListMessageBuilder(t *testing.T) {
	provider := todo.New(&todo.Options{
		TodoListMessageBuilder: func(items []todo.Item) string {
			return "CUSTOM: empty"
		},
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

	secondText := outMessages[1].Contents.Text()
	if !strings.Contains(secondText, "CUSTOM:") {
		t.Error("expected custom todo list message builder output")
	}
}
