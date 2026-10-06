// Copyright (c) Microsoft. All rights reserved.

package todo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/messagetest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func newMessages(text string) []*message.Message {
	return []*message.Message{message.NewText(text)}
}

func sessionOpts() []agent.Option {
	return []agent.Option{agent.WithSession(agenttest.CreateSession())}
}

func mustSession(t *testing.T, opts []agent.Option) *agent.Session {
	t.Helper()
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok || session == nil {
		t.Fatal("expected a session option")
	}
	return session
}

func invokeProvider(provider *todo.Provider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
}

func collectTools(opts []agent.Option) []tool.Tool {
	var tools []tool.Tool
	for _, opt := range opts {
		if tt, ok := opt.MAFValue().(tool.Tool); ok {
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

func callTool(t *testing.T, opts []agent.Option, name string, argsJSON string) string {
	t.Helper()
	var tools []tool.Tool
	for _, opt := range opts {
		if tt, ok := opt.MAFValue().(tool.Tool); ok {
			tools = append(tools, tt)
		}
	}
	for _, tt := range tools {
		if tt.Name() == name {
			ft, ok := tt.(tool.FuncTool)
			if !ok {
				t.Fatalf("tool %s is not a FuncTool", name)
			}
			result, err := ft.Call(context.Background(), argsJSON)
			if err != nil {
				t.Fatalf("tool %s call failed: %v", name, err)
			}
			return fmt.Sprintf("%v", result)
		}
	}
	t.Fatalf("tool %q not found", name)
	return ""
}

// 1. ProvideAIContextAsync_ReturnsToolsAndInstructions
func TestProvide_ReturnsToolsAndInstructions(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	tools := collectTools(outOpts)
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}

	instructions := collectInstructions(outOpts)
	if instructions == "" {
		t.Fatal("expected non-empty instructions")
	}
	// Default instructions mirror the current .NET TodoProvider text: a
	// numbered simple-vs-complex decision and a General TODO Guidelines heading.
	for _, want := range []string{
		"### General TODO Guidelines",
		"just complete the task directly",
	} {
		if !strings.Contains(instructions, want) {
			t.Errorf("expected default instructions to contain %q", want)
		}
	}
}

// 2. AddTodos_CreatesSingleItem
func TestAddTodos_CreatesSingleItem(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Buy milk","description":"A test description"}]}`)

	items := p.AllTodos(mustSession(t, opts))
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Buy milk" {
		t.Errorf("expected 'Buy milk', got %q", items[0].Title)
	}
	if items[0].Description != "A test description" {
		t.Errorf("description = %q, want A test description", items[0].Description)
	}
	if items[0].IsComplete {
		t.Error("expected a newly added item to be incomplete")
	}
	// Numbering starts at 1, matching the .NET/Python harnesses.
	if items[0].ID != 1 {
		t.Errorf("first todo ID = %d, want 1", items[0].ID)
	}
}

// 3. AddTodos_CreatesMultipleItemsWithIncrementingIds
func TestAddTodos_CreatesMultipleItems(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Item 1"},{"title":"Item 2"},{"title":"Item 3","description":"With description"}]}`)

	items := p.AllTodos(mustSession(t, opts))
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].ID >= items[1].ID || items[1].ID >= items[2].ID {
		t.Error("expected incrementing IDs")
	}
	for i, title := range []string{"Item 1", "Item 2", "Item 3"} {
		if items[i].ID != i+1 {
			t.Errorf("item %d ID = %d, want %d", i, items[i].ID, i+1)
		}
		if items[i].Title != title {
			t.Errorf("item %d title = %q, want %q", i, items[i].Title, title)
		}
	}
	if items[2].Description != "With description" {
		t.Errorf("third item description = %q, want With description", items[2].Description)
	}
}

// 4. CompleteTodos_MarksItemComplete
func TestCompleteTodos_MarksItemComplete(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Task A"}]}`)
	items := p.AllTodos(mustSession(t, opts))
	id := items[0].ID

	result := callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, id))
	if result != "1" {
		t.Errorf("expected 1 completed, got %s", result)
	}

	items = p.AllTodos(mustSession(t, opts))
	if !items[0].IsComplete {
		t.Error("expected item to be complete")
	}
}

// 5. CompleteTodos_MarksMultipleItemsComplete
func TestCompleteTodos_MarksMultipleComplete(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"A"},{"title":"B"},{"title":"C"}]}`)
	items := p.AllTodos(mustSession(t, opts))

	result := callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"},{"id":%d,"reason":"done"}]}`, items[0].ID, items[2].ID))
	if result != "2" {
		t.Errorf("expected 2 completed, got %s", result)
	}

	remaining := p.RemainingTodos(mustSession(t, opts))
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Title != "B" {
		t.Errorf("expected 'B' remaining, got %q", remaining[0].Title)
	}
	items = p.AllTodos(mustSession(t, opts))
	if len(items) != 3 {
		t.Fatalf("expected 3 items after completion, got %d", len(items))
	}
	if !items[0].IsComplete || items[1].IsComplete || !items[2].IsComplete {
		t.Errorf("completion states = %v/%v/%v, want true/false/true", items[0].IsComplete, items[1].IsComplete, items[2].IsComplete)
	}
}

// 6. CompleteTodos_ReturnsZeroForMissingIds
func TestCompleteTodos_ReturnsZeroForMissingIds(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	result := callTool(t, outOpts, "todos_complete", `{"Arg0":[{"id":999,"reason":"done"}]}`)
	if result != "0" {
		t.Errorf("expected 0 completed for missing ID, got %s", result)
	}
}

// 7. RemoveTodos_RemovesItem
func TestRemoveTodos_RemovesItem(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Remove me"}]}`)
	items := p.AllTodos(mustSession(t, opts))
	if len(items) != 1 {
		t.Fatalf("expected 1 item before remove, got %d", len(items))
	}
	id := items[0].ID
	if id != 1 {
		t.Fatalf("first todo ID = %d, want 1", id)
	}

	result := callTool(t, outOpts, "todos_remove", fmt.Sprintf(`{"Arg0":[%d]}`, id))
	if result != "1" {
		t.Errorf("expected 1 removed, got %s", result)
	}

	items = p.AllTodos(mustSession(t, opts))
	if len(items) != 0 {
		t.Fatalf("expected 0 items after remove, got %d", len(items))
	}
}

// 8. RemoveTodos_RemovesMultipleItems
func TestRemoveTodos_RemovesMultipleItems(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"A"},{"title":"B"},{"title":"C"}]}`)
	items := p.AllTodos(mustSession(t, opts))

	result := callTool(t, outOpts, "todos_remove", fmt.Sprintf(`{"Arg0":[%d,%d]}`, items[0].ID, items[2].ID))
	if result != "2" {
		t.Errorf("expected 2 removed, got %s", result)
	}

	items = p.AllTodos(mustSession(t, opts))
	if len(items) != 1 {
		t.Fatalf("expected 1 item remaining, got %d", len(items))
	}
	if items[0].Title != "B" {
		t.Errorf("expected 'B', got %q", items[0].Title)
	}
}

// 9. RemoveTodos_ReturnsZeroForMissingIds
func TestRemoveTodos_ReturnsZeroForMissingIds(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	result := callTool(t, outOpts, "todos_remove", `{"Arg0":[999]}`)
	if result != "0" {
		t.Errorf("expected 0 removed for missing ID, got %s", result)
	}
}

// 10. RemainingTodos_ReturnsOnlyIncomplete
func TestRemainingTodos_ReturnsOnlyIncomplete(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Done"},{"title":"Pending"}]}`)
	items := p.AllTodos(mustSession(t, opts))
	if len(items) != 2 {
		t.Fatalf("expected 2 items before completion, got %d", len(items))
	}
	if items[0].ID != 1 {
		t.Fatalf("first todo ID = %d, want 1", items[0].ID)
	}
	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, items[0].ID))

	remaining := p.RemainingTodos(mustSession(t, opts))
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Title != "Pending" {
		t.Errorf("expected 'Pending', got %q", remaining[0].Title)
	}
}

// 11. AllTodos_ReturnsAllItems
func TestAllTodos_ReturnsAllItems(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Done"},{"title":"Pending"}]}`)
	items := p.AllTodos(mustSession(t, opts))
	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, items[0].ID))

	all := p.AllTodos(mustSession(t, opts))
	if len(all) != 2 {
		t.Fatalf("expected 2 items, got %d", len(all))
	}
}

// 12. State_PersistsInSessionStateBag
func TestState_PersistsAcrossInvocations(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	// First invocation: add items.
	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Persist me"}]}`)

	// Second invocation: items should still be there.
	_, outOpts2, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	_ = outOpts2

	items := p.AllTodos(mustSession(t, opts))
	if len(items) != 1 {
		t.Fatalf("expected 1 item to persist, got %d", len(items))
	}
	if items[0].Title != "Persist me" {
		t.Errorf("expected 'Persist me', got %q", items[0].Title)
	}
}

// 13. PublicAllTodos_ReturnsAllItems
func TestPublicAllTodos_ReturnsAllItems(t *testing.T) {
	p := todo.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"X"},{"title":"Y"}]}`)

	all := p.AllTodos(session)
	if len(all) != 2 {
		t.Fatalf("expected 2 items, got %d", len(all))
	}
	if all[0].Title != "X" || all[1].Title != "Y" {
		t.Errorf("titles = %q/%q, want X/Y", all[0].Title, all[1].Title)
	}
}

// 14. PublicRemainingTodos_ReturnsOnlyIncomplete
func TestPublicRemainingTodos_ReturnsOnlyIncomplete(t *testing.T) {
	p := todo.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Done"},{"title":"Open"}]}`)
	items := p.AllTodos(session)
	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, items[0].ID))

	remaining := p.RemainingTodos(session)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Title != "Open" {
		t.Errorf("expected 'Open', got %q", remaining[0].Title)
	}
}

// 15. PublicAllTodos_ReturnsEmptyForNewSession
func TestPublicAllTodos_ReturnsEmptyForNewSession(t *testing.T) {
	p := todo.New(nil)
	session := agenttest.CreateSession()

	items := p.AllTodos(session)
	if len(items) != 0 {
		t.Fatalf("expected 0 items for new session, got %d", len(items))
	}
}

func TestPublicAllTodosFromNilSession_ReturnsEmpty(t *testing.T) {
	p := todo.New(nil)

	items := p.AllTodos(nil)
	if len(items) != 0 {
		t.Fatalf("expected 0 items for nil session, got %d", len(items))
	}
}

// 16. Options_CustomInstructions_OverridesDefault
func TestCustomInstructions_OverridesDefault(t *testing.T) {
	p := todo.New(&todo.Options{
		Instructions: new("Custom todo instructions here"),
	})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if instructions != "Custom todo instructions here" {
		t.Errorf("instructions = %q, want %q", instructions, "Custom todo instructions here")
	}
}

// 17. Options_Null_UsesDefaultInstructions
func TestNilOptions_UsesDefaultInstructions(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "Todo") {
		t.Error("expected default instructions to contain 'Todo'")
	}
	if !strings.Contains(instructions, "todo list") {
		t.Error("expected default instructions to describe the todo list")
	}
}

// 18. ProvideAIContextAsync_InjectsEmptyTodoMessage
func TestProvide_InjectsEmptyTodoMessage(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	outMessages, _, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	if len(outMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(outMessages))
	}
	text := outMessages[1].String()
	if !strings.Contains(text, "### Current todo list") || !strings.Contains(text, "none yet") {
		t.Errorf("expected current todo list heading and 'none yet' in injected message, got %q", text)
	}
}

// 19. ProvideAIContextAsync_InjectsTodoListMessage
func TestProvide_InjectsTodoListMessage(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	// First call to get tools and add items.
	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Task A"},{"title":"Task B","description":"Has details"}]}`)
	items := p.AllTodos(mustSession(t, opts))
	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, items[0].ID))

	// Second call should inject todo list message.
	outMessages, _, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	if len(outMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(outMessages))
	}
	text := outMessages[1].String()
	if !strings.Contains(text, "### Current todo list") {
		t.Errorf("expected current todo list heading in injected message, got %q", text)
	}
	if !strings.Contains(text, "[done] Task A") {
		t.Errorf("expected '[done] Task A' in injected message, got %q", text)
	}
	if !strings.Contains(text, "[open] Task B") {
		t.Errorf("expected '[open] Task B' in injected message, got %q", text)
	}
	if !strings.Contains(text, ": Has details") {
		t.Errorf("expected description suffix in injected message, got %q", text)
	}
}

func TestProvide_FormatsPersistedDescriptions(t *testing.T) {
	// Deserialize the session so the provider can decode its private state type.
	var session agent.Session
	err := json.Unmarshal([]byte(`{
		"State": {
			"todoProviderState": {
				"nextId": 3,
				"items": [
					{"id": 1, "title": "Blank description", "description": "   ", "isComplete": false},
					{"id": 2, "title": "Detailed", "description": "details", "isComplete": true}
				]
			}
		}
	}`), &session)
	if err != nil {
		t.Fatal(err)
	}

	p := todo.New(nil)
	outMessages, _, err := invokeProvider(
		p,
		context.Background(),
		newMessages("hi"),
		agent.WithSession(&session),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := "### Current todo list\n- 1 [open] Blank description\n- 2 [done] Detailed: details"
	for _, msg := range outMessages {
		if text := msg.Contents.Text(); strings.HasPrefix(text, "### Current todo list") {
			if text != want {
				t.Fatalf("todo list message = %q, want %q", text, want)
			}
			return
		}
	}
	t.Fatal("expected current todo list message")
}

// 20. ProvideAIContextAsync_SuppressTodoListMessage_NoMessageInjected
func TestProvide_SuppressTodoListMessage(t *testing.T) {
	p := todo.New(&todo.Options{
		SuppressTodoListMessage: true,
	})
	opts := sessionOpts()
	msgs := newMessages("hi")

	outMessages, _, err := invokeProvider(p, context.Background(), msgs, opts...)
	if err != nil {
		t.Fatal(err)
	}

	if err := messagetest.MessagesEqual(outMessages, newMessages("hi")); err != nil {
		t.Errorf("expected only original messages with suppressed todo list: %v", err)
	}
}

// 21. ProvideAIContextAsync_CustomTodoListMessageBuilder
func TestProvide_CustomTodoListMessageBuilder(t *testing.T) {
	p := todo.New(&todo.Options{
		TodoListMessageBuilder: func(items []todo.Item) string {
			return "CUSTOM: empty"
		},
	})
	opts := sessionOpts()

	outMessages, _, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, msg := range outMessages {
		if strings.Contains(msg.Contents.Text(), "CUSTOM:") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected custom builder output in messages")
	}
}

// 22. ProvideAIContextAsync_SuppressWinsOverBuilder
func TestProvide_SuppressWinsOverBuilder(t *testing.T) {
	var builderCalled bool
	p := todo.New(&todo.Options{
		SuppressTodoListMessage: true,
		TodoListMessageBuilder: func(items []todo.Item) string {
			builderCalled = true
			return "CUSTOM: should not appear"
		},
	})
	opts := sessionOpts()
	msgs := newMessages("hi")

	outMessages, _, err := invokeProvider(p, context.Background(), msgs, opts...)
	if err != nil {
		t.Fatal(err)
	}

	if builderCalled {
		t.Error("suppressed todo list should not invoke the custom builder")
	}
	if err := messagetest.MessagesEqual(outMessages, newMessages("hi")); err != nil {
		t.Errorf("expected only original messages with suppressed todo list: %v", err)
	}
}

// Verify tool names.
func TestToolNames(t *testing.T) {
	p := todo.New(nil)
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

	expected := []string{"todos_add", "todos_complete", "todos_remove", "todos_get_remaining", "todos_get_all"}
	for _, name := range expected {
		if !slices.Contains(names, name) {
			t.Errorf("expected tool %q not found", name)
		}
	}
}

// Verify completion input with a reason is accepted and items are marked complete.
func TestCompleteTodos_WithReason(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Task X"}]}`)
	items := p.AllTodos(mustSession(t, opts))
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	result := callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"completed successfully"}]}`, items[0].ID))
	if result != "1" {
		t.Errorf("expected 1 completed, got %s", result)
	}

	all := p.AllTodos(mustSession(t, opts))
	if !all[0].IsComplete {
		t.Error("expected item to be complete after providing reason")
	}
}

// Verify that todos_complete description mentions reason.
func TestCompleteToolDescription_MentionsReason(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	tools := collectTools(outOpts)
	for _, tt := range tools {
		if tt.Name() == "todos_complete" {
			if !strings.Contains(tt.Description(), "reason") {
				t.Errorf("expected todos_complete description to mention 'reason', got: %s", tt.Description())
			}
			return
		}
	}
	t.Error("todos_complete tool not found")
}

// TestCompleteTodos_EmptyReasonIsAccepted verifies that todos_complete allows
// an empty or omitted reason, matching the upstream .NET behavior where the reason
// field is prompted for but not enforced at runtime.
func TestCompleteTodos_EmptyReasonIsAccepted(t *testing.T) {
	cases := []struct {
		name   string
		reason string
	}{
		{"empty reason", ""},
		{"whitespace reason", "   "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := todo.New(nil)
			opts := sessionOpts()

			_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
			if err != nil {
				t.Fatal(err)
			}

			callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Task Z"}]}`)
			items := p.AllTodos(mustSession(t, opts))
			if len(items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(items))
			}

			result := callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":%q}]}`, items[0].ID, tc.reason))
			if !strings.Contains(result, "1") {
				t.Errorf("expected 1 completed with %q reason, got %s", tc.reason, result)
			}

			all := p.AllTodos(mustSession(t, opts))
			if !all[0].IsComplete {
				t.Errorf("item should be complete even with %q reason", tc.reason)
			}
		})
	}
}

// Concurrent tool invocations and public reads on a shared session must be
// serialized by the per-session lock rather than race on the session's todo
// state. Run under -race.
func TestTodo_ConcurrentSessionAccess_NoDataRace(t *testing.T) {
	p := todo.New(nil)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	var addTool tool.FuncTool
	for _, tt := range collectTools(outOpts) {
		if tt.Name() == "todos_add" {
			addTool, _ = tt.(tool.FuncTool)
		}
	}
	if addTool == nil {
		t.Fatal("todos_add tool not found")
	}

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n * 2)
	errs := make([]error, n*2)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = addTool.Call(context.Background(), fmt.Sprintf(`{"Arg0":[{"title":"item-%d"}]}`, idx))
		}(i * 2)
		go func(idx int) {
			defer wg.Done()
			_ = p.AllTodos(session)
			_ = p.RemainingTodos(session)
		}(i*2 + 1)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatalf("concurrent todos_add failed: %v", e)
		}
	}
}
