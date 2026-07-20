// Copyright (c) Microsoft. All rights reserved.

package todo_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
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

func invokeProvider(provider *todo.Provider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
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

// addItems is a helper that uses the public API to set up todo items via session state.
// It calls Invoking to initialize tools, then uses GetAllItems to verify.
// Since we can't call tools directly, we manipulate state through the provider's public methods.
// Instead, we add items by calling Invoking which creates tools bound to the session,
// then we inspect state. For actual item creation, we rely on integration through Invoking.
//
// For tests that need items, we'll use a workaround: call Invoking to get the tools,
// then invoke the tools via their Call method.
func callTool(t *testing.T, opts []agent.Option, name string, argsJSON string) string {
	t.Helper()
	var tools []tool.Tool
	for _, opt := range opts {
		if tt, ok := opt.Value().(tool.Tool); ok {
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
}

// 2. AddTodos_CreatesSingleItem
func TestAddTodos_CreatesSingleItem(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Buy milk"}]}`)

	items := p.GetAllItems(opts...)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Buy milk" {
		t.Errorf("expected 'Buy milk', got %q", items[0].Title)
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

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Item 1"},{"title":"Item 2"},{"title":"Item 3"}]}`)

	items := p.GetAllItems(opts...)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].ID >= items[1].ID || items[1].ID >= items[2].ID {
		t.Error("expected incrementing IDs")
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
	items := p.GetAllItems(opts...)
	id := items[0].ID

	result := callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, id))
	if !strings.Contains(result, "1") {
		t.Errorf("expected 1 completed, got %s", result)
	}

	items = p.GetAllItems(opts...)
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
	items := p.GetAllItems(opts...)

	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"},{"id":%d,"reason":"done"}]}`, items[0].ID, items[1].ID))

	remaining := p.GetRemainingItems(opts...)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Title != "C" {
		t.Errorf("expected 'C' remaining, got %q", remaining[0].Title)
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
	if !strings.Contains(result, "0") {
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
	items := p.GetAllItems(opts...)
	id := items[0].ID

	callTool(t, outOpts, "todos_remove", fmt.Sprintf(`{"Arg0":[%d]}`, id))

	items = p.GetAllItems(opts...)
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
	items := p.GetAllItems(opts...)

	callTool(t, outOpts, "todos_remove", fmt.Sprintf(`{"Arg0":[%d,%d]}`, items[0].ID, items[1].ID))

	items = p.GetAllItems(opts...)
	if len(items) != 1 {
		t.Fatalf("expected 1 item remaining, got %d", len(items))
	}
	if items[0].Title != "C" {
		t.Errorf("expected 'C', got %q", items[0].Title)
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
	if !strings.Contains(result, "0") {
		t.Errorf("expected 0 removed for missing ID, got %s", result)
	}
}

// 10. GetRemainingTodos_ReturnsOnlyIncomplete
func TestGetRemainingTodos_ReturnsOnlyIncomplete(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Done"},{"title":"Pending"}]}`)
	items := p.GetAllItems(opts...)
	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, items[0].ID))

	remaining := p.GetRemainingItems(opts...)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Title != "Pending" {
		t.Errorf("expected 'Pending', got %q", remaining[0].Title)
	}
}

// 11. GetAllTodos_ReturnsAllItems
func TestGetAllTodos_ReturnsAllItems(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Done"},{"title":"Pending"}]}`)
	items := p.GetAllItems(opts...)
	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, items[0].ID))

	all := p.GetAllItems(opts...)
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

	items := p.GetAllItems(opts...)
	if len(items) != 1 {
		t.Fatalf("expected 1 item to persist, got %d", len(items))
	}
	if items[0].Title != "Persist me" {
		t.Errorf("expected 'Persist me', got %q", items[0].Title)
	}
}

// 13. PublicGetAllTodos_ReturnsAllItems
func TestPublicGetAllTodos_ReturnsAllItems(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"X"},{"title":"Y"}]}`)

	all := p.GetAllItems(opts...)
	if len(all) != 2 {
		t.Fatalf("expected 2 items, got %d", len(all))
	}
}

// 14. PublicGetRemainingTodos_ReturnsOnlyIncomplete
func TestPublicGetRemainingTodos_ReturnsOnlyIncomplete(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Done"},{"title":"Open"}]}`)
	items := p.GetAllItems(opts...)
	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, items[0].ID))

	remaining := p.GetRemainingItems(opts...)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Title != "Open" {
		t.Errorf("expected 'Open', got %q", remaining[0].Title)
	}
}

// 15. PublicGetAllTodos_ReturnsEmptyForNewSession
func TestPublicGetAllTodos_ReturnsEmptyForNewSession(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	items := p.GetAllItems(opts...)
	if len(items) != 0 {
		t.Fatalf("expected 0 items for new session, got %d", len(items))
	}
}

// 16. Options_CustomInstructions_OverridesDefault
func TestCustomInstructions_OverridesDefault(t *testing.T) {
	p := todo.New(&todo.Options{
		Instructions: "Custom todo instructions here",
	})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	instructions := collectInstructions(outOpts)
	if !strings.Contains(instructions, "Custom todo instructions") {
		t.Error("expected custom instructions")
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
}

// 18. ProvideAIContextAsync_InjectsEmptyTodoMessage
func TestProvide_InjectsEmptyTodoMessage(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	outMessages, _, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, msg := range outMessages {
		if strings.Contains(msg.Contents.Text(), "none yet") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'none yet' in messages for empty todo list")
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
	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Task A"},{"title":"Task B"}]}`)
	items := p.GetAllItems(opts...)
	callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"done"}]}`, items[0].ID))

	// Second call should inject todo list message.
	outMessages, _, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	foundDone := false
	foundOpen := false
	for _, msg := range outMessages {
		text := msg.Contents.Text()
		if strings.Contains(text, "[done]") {
			foundDone = true
		}
		if strings.Contains(text, "[open]") {
			foundOpen = true
		}
	}
	if !foundDone {
		t.Error("expected '[done]' in todo list message")
	}
	if !foundOpen {
		t.Error("expected '[open]' in todo list message")
	}
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

	// With suppression, no todo list message should be injected.
	// The output messages should be the same length as input (no extra todo message).
	if len(outMessages) != len(msgs) {
		t.Errorf("expected %d messages with suppressed todo, got %d", len(msgs), len(outMessages))
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
	p := todo.New(&todo.Options{
		SuppressTodoListMessage: true,
		TodoListMessageBuilder: func(items []todo.Item) string {
			return "CUSTOM: should not appear"
		},
	})
	opts := sessionOpts()
	msgs := newMessages("hi")

	outMessages, _, err := invokeProvider(p, context.Background(), msgs, opts...)
	if err != nil {
		t.Fatal(err)
	}

	for _, msg := range outMessages {
		if strings.Contains(msg.Contents.Text(), "CUSTOM:") {
			t.Error("suppress should win over builder")
		}
	}
	if len(outMessages) != len(msgs) {
		t.Errorf("expected %d messages with suppress, got %d", len(msgs), len(outMessages))
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

// Verify CompleteInput with reason is accepted and items are marked complete.
func TestCompleteTodos_WithReason(t *testing.T) {
	p := todo.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "todos_add", `{"Arg0":[{"title":"Task X"}]}`)
	items := p.GetAllItems(opts...)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	result := callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":"completed successfully"}]}`, items[0].ID))
	if !strings.Contains(result, "1") {
		t.Errorf("expected 1 completed, got %s", result)
	}

	all := p.GetAllItems(opts...)
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
			items := p.GetAllItems(opts...)
			if len(items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(items))
			}

			result := callTool(t, outOpts, "todos_complete", fmt.Sprintf(`{"Arg0":[{"id":%d,"reason":%q}]}`, items[0].ID, tc.reason))
			if !strings.Contains(result, "1") {
				t.Errorf("expected 1 completed with %q reason, got %s", tc.reason, result)
			}

			all := p.GetAllItems(opts...)
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
	opts := sessionOpts()

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
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = addTool.Call(context.Background(), fmt.Sprintf(`{"Arg0":[{"title":"item-%d"}]}`, idx))
		}(i * 2)
		go func(idx int) {
			defer wg.Done()
			_ = p.GetAllItems(opts...)
			_ = p.GetRemainingItems(opts...)
		}(i*2 + 1)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatalf("concurrent todos_add failed: %v", e)
		}
	}
}
