// Copyright (c) Microsoft. All rights reserved.

package filememory_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/filememory"
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

func invokeProvider(provider *filememory.Provider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
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

func callTool(t *testing.T, opts []agent.Option, name string, argsJSON string) string {
	t.Helper()
	for _, tt := range collectTools(opts) {
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

// Invoking returns the five file_memory_* tools as WithTool options plus
// non-empty instructions.
func TestProvide_ReturnsToolsAndInstructions(t *testing.T) {
	p := filememory.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	tools := collectTools(outOpts)
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}

	names := make([]string, len(tools))
	for i, tt := range tools {
		names[i] = tt.Name()
	}
	expected := []string{
		"file_memory_write",
		"file_memory_read",
		"file_memory_delete",
		"file_memory_ls",
		"file_memory_grep",
	}
	for _, name := range expected {
		if !slices.Contains(names, name) {
			t.Errorf("expected tool %q not found in %v", name, names)
		}
	}

	if collectInstructions(outOpts) == "" {
		t.Fatal("expected non-empty instructions")
	}
}

func TestNilOptions_UsesDefaultInstructions(t *testing.T) {
	p := filememory.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(collectInstructions(outOpts), "File Memory") {
		t.Error("expected default instructions to contain 'File Memory'")
	}
}

func TestCustomInstructions_OverridesDefault(t *testing.T) {
	p := filememory.New(&filememory.Options{Instructions: "Custom file memory instructions"})
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(collectInstructions(outOpts), "Custom file memory instructions") {
		t.Error("expected custom instructions")
	}
}

// Drive the in-memory store through write -> read -> ls -> grep and assert
// content and behavior.
func TestWriteReadLsGrep(t *testing.T) {
	p := filememory.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "file_memory_write", `{"path":"notes.txt","content":"hello world"}`)
	callTool(t, outOpts, "file_memory_write", `{"path":"todo.md","content":"buy milk"}`)

	read := callTool(t, outOpts, "file_memory_read", `{"Arg0":"notes.txt"}`)
	if read != "hello world" {
		t.Errorf("expected 'hello world', got %q", read)
	}

	// Reading a missing file returns empty string.
	missing := callTool(t, outOpts, "file_memory_read", `{"Arg0":"nope.txt"}`)
	if missing != "" {
		t.Errorf("expected empty for missing file, got %q", missing)
	}

	ls := callTool(t, outOpts, "file_memory_ls", `{}`)
	if !strings.Contains(ls, "notes.txt") || !strings.Contains(ls, "todo.md") {
		t.Errorf("expected ls to list both files, got %q", ls)
	}

	grep := callTool(t, outOpts, "file_memory_grep", `{"pattern":"world"}`)
	if !strings.Contains(grep, "notes.txt") {
		t.Errorf("expected grep to match notes.txt, got %q", grep)
	}
	if strings.Contains(grep, "todo.md") {
		t.Errorf("expected grep NOT to match todo.md, got %q", grep)
	}

	if paths := p.List(opts...); len(paths) != 2 {
		t.Errorf("expected 2 files via List, got %d", len(paths))
	}
}

func TestWriteOverwrites(t *testing.T) {
	p := filememory.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "file_memory_write", `{"path":"a.txt","content":"first"}`)
	callTool(t, outOpts, "file_memory_write", `{"path":"a.txt","content":"second"}`)

	read := callTool(t, outOpts, "file_memory_read", `{"Arg0":"a.txt"}`)
	if read != "second" {
		t.Errorf("expected overwrite to 'second', got %q", read)
	}
	if paths := p.List(opts...); len(paths) != 1 {
		t.Errorf("expected 1 file after overwrite, got %d", len(paths))
	}
}

func TestDelete(t *testing.T) {
	p := filememory.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	callTool(t, outOpts, "file_memory_write", `{"path":"gone.txt","content":"data"}`)

	removed := callTool(t, outOpts, "file_memory_delete", `{"Arg0":"gone.txt"}`)
	if !strings.Contains(removed, "true") {
		t.Errorf("expected delete to report true, got %q", removed)
	}
	if paths := p.List(opts...); len(paths) != 0 {
		t.Errorf("expected 0 files after delete, got %d", len(paths))
	}

	// Deleting a missing file reports false.
	missing := callTool(t, outOpts, "file_memory_delete", `{"Arg0":"gone.txt"}`)
	if !strings.Contains(missing, "false") {
		t.Errorf("expected delete of missing file to report false, got %q", missing)
	}
}

// State persists across a second Invoking via the session state bag.
func TestState_PersistsAcrossInvocations(t *testing.T) {
	p := filememory.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	callTool(t, outOpts, "file_memory_write", `{"path":"persist.txt","content":"keep me"}`)

	// Second invocation: rebuild tools bound to the same session.
	_, outOpts2, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	read := callTool(t, outOpts2, "file_memory_read", `{"Arg0":"persist.txt"}`)
	if read != "keep me" {
		t.Errorf("expected persisted content 'keep me', got %q", read)
	}
	if paths := p.List(opts...); len(paths) != 1 {
		t.Errorf("expected 1 persisted file, got %d", len(paths))
	}
}

func TestList_EmptyForNewSession(t *testing.T) {
	p := filememory.New(nil)
	if paths := p.List(sessionOpts()...); len(paths) != 0 {
		t.Errorf("expected 0 files for new session, got %d", len(paths))
	}
}

func TestGrep_InvalidPatternReturnsError(t *testing.T) {
	p := filememory.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}

	var grepTool tool.FuncTool
	for _, tt := range collectTools(outOpts) {
		if tt.Name() == "file_memory_grep" {
			grepTool, _ = tt.(tool.FuncTool)
		}
	}
	if grepTool == nil {
		t.Fatal("file_memory_grep tool not found")
	}
	if _, err := grepTool.Call(context.Background(), `{"pattern":"["}`); err == nil {
		t.Error("expected error for invalid regexp pattern")
	}
}

// Concurrent writes and public reads on a shared session must be serialized by
// the per-session lock rather than race on the session's file store. Run under
// -race.
func TestFileMemory_ConcurrentSessionAccess_NoDataRace(t *testing.T) {
	p := filememory.New(nil)
	opts := sessionOpts()

	_, outOpts, err := invokeProvider(p, context.Background(), newMessages("hi"), opts...)
	if err != nil {
		t.Fatal(err)
	}
	var writeTool tool.FuncTool
	for _, tt := range collectTools(outOpts) {
		if tt.Name() == "file_memory_write" {
			writeTool, _ = tt.(tool.FuncTool)
		}
	}
	if writeTool == nil {
		t.Fatal("file_memory_write tool not found")
	}

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n * 2)
	errs := make([]error, n*2)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = writeTool.Call(context.Background(), fmt.Sprintf(`{"path":"f-%d.txt","content":"c-%d"}`, idx, idx))
		}(i * 2)
		go func(idx int) {
			defer wg.Done()
			_ = p.List(opts...)
		}(i*2 + 1)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatalf("concurrent file_memory_write failed: %v", e)
		}
	}
}
