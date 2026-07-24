// Copyright (c) Microsoft. All rights reserved.

package filememory_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/filememory"
	"github.com/microsoft/agent-framework-go/agent/harness/filestore"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/tool"
)

func TestNewPanicsWithNilStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = filememory.New(nil, nil, nil)
}

func TestProviderInvokingReturnsToolsInstructionsAndIndex(t *testing.T) {
	provider := filememory.New(filestore.NewInMemoryStore(), nil, nil)
	opts := []agent.Option{agent.WithSession(agenttest.CreateSession())}

	messages, outOpts, err := provider.Invoking(context.Background(), agent.InvokingContext{Options: opts})
	if err != nil {
		t.Fatalf("Invoking() error = %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("initial messages length = %d, want 0", len(messages))
	}
	if got := toolNames(outOpts); !slices.Equal(got, []string{
		filememory.WriteToolName,
		filememory.ReadToolName,
		filememory.DeleteToolName,
		filememory.LsToolName,
		filememory.GrepToolName,
		filememory.ReplaceToolName,
		filememory.ReplaceLinesToolName,
	}) {
		t.Fatalf("tool names = %v", got)
	}
	instructions := collectInstructions(outOpts)
	for _, want := range []string{"file-based memory", "file_memory_ls", "file_memory_replace_lines"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing %q: %q", want, instructions)
		}
	}

	callTool(t, outOpts, filememory.WriteToolName, `{"file_name":"notes.md","content":"remember this","description":"Short note"}`)

	messages, _, err = provider.Invoking(context.Background(), agent.InvokingContext{Options: opts})
	if err != nil {
		t.Fatalf("Invoking() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(messages))
	}
	if messages[0].Source.Type != agent.SourceTypeContextProvider {
		t.Fatalf("message source type = %q", messages[0].Source.Type)
	}
	if got := messages[0].String(); !strings.Contains(got, "# Memory Index") || !strings.Contains(got, "**notes.md**: Short note") {
		t.Fatalf("index message = %q", got)
	}
}

func TestProviderToolsManageFiles(t *testing.T) {
	provider := filememory.New(filestore.NewInMemoryStore(), nil, nil)
	opts := []agent.Option{agent.WithSession(agenttest.CreateSession())}
	_, outOpts, err := provider.Invoking(context.Background(), agent.InvokingContext{Options: opts})
	if err != nil {
		t.Fatalf("Invoking() error = %v", err)
	}

	if got := callTool(t, outOpts, filememory.WriteToolName, `{"file_name":"notes.md","content":"line one\nline two\nline two\n","description":"Trip notes"}`); got != `File "notes.md" written with description.` {
		t.Fatalf("write result = %q", got)
	}
	if got := callTool(t, outOpts, filememory.ReadToolName, `{"file_name":"notes.md"}`); got != "line one\nline two\nline two\n" {
		t.Fatalf("read result = %q", got)
	}

	ls := callToolAny(t, outOpts, filememory.LsToolName, `{"glob_pattern":"*.md"}`).([]filememory.ListEntry)
	wantLS := []filememory.ListEntry{{Name: "notes.md", Type: filestore.EntryTypeFile, Description: "Trip notes"}}
	if !slices.Equal(ls, wantLS) {
		t.Fatalf("ls result = %#v, want %#v", ls, wantLS)
	}

	grep := callToolAny(t, outOpts, filememory.GrepToolName, `{"regex_pattern":"line two"}`).([]filestore.SearchResult)
	if len(grep) != 1 || grep[0].FileName != "notes.md" || len(grep[0].MatchingLines) != 2 {
		t.Fatalf("grep result = %#v", grep)
	}

	if got := callTool(t, outOpts, filememory.ReplaceToolName, `{"file_name":"notes.md","old_string":"line one","new_string":"first line"}`); got != `Replaced 1 occurrence(s) in "notes.md".` {
		t.Fatalf("replace result = %q", got)
	}
	if got := callTool(t, outOpts, filememory.ReplaceLinesToolName, `{"file_name":"notes.md","edits":[{"line_number":2,"new_line":"updated line two\n"},{"line_number":3,"new_line":""}]}`); got != `Replaced 2 line(s) in "notes.md".` {
		t.Fatalf("replace_lines result = %q", got)
	}
	if got := callTool(t, outOpts, filememory.ReadToolName, `{"file_name":"notes.md"}`); got != "first line\nupdated line two\n" {
		t.Fatalf("read after edits = %q", got)
	}
	if got := callTool(t, outOpts, filememory.DeleteToolName, `{"file_name":"notes.md"}`); got != `File "notes.md" deleted.` {
		t.Fatalf("delete result = %q", got)
	}
	if got := callTool(t, outOpts, filememory.ReadToolName, `{"file_name":"notes.md"}`); got != `File "notes.md" not found.` {
		t.Fatalf("read after delete = %q", got)
	}
}

func TestProviderRejectsNestedAndReservedNames(t *testing.T) {
	provider := filememory.New(filestore.NewInMemoryStore(), nil, nil)
	opts := []agent.Option{agent.WithSession(agenttest.CreateSession())}
	_, outOpts, err := provider.Invoking(context.Background(), agent.InvokingContext{Options: opts})
	if err != nil {
		t.Fatalf("Invoking() error = %v", err)
	}

	if _, err := findTool(t, outOpts, filememory.WriteToolName).Call(context.Background(), `{"file_name":"nested/file.md","content":"bad"}`); err == nil {
		t.Fatal("expected nested path error")
	}
	if _, err := findTool(t, outOpts, filememory.WriteToolName).Call(context.Background(), `{"file_name":"memories.md","content":"bad"}`); err == nil {
		t.Fatal("expected reserved name error")
	}
}

func TestProviderUsesWorkingFolderStateInitializer(t *testing.T) {
	store := filestore.NewInMemoryStore()
	provider := filememory.New(store, func(session *agent.Session) filememory.State {
		return filememory.State{WorkingFolder: session.ServiceID()}
	}, nil)

	sessionA := agenttest.CreateSession()
	sessionA.SetServiceID("user-a")
	sessionB := agenttest.CreateSession()
	sessionB.SetServiceID("user-b")

	_, optsA, err := provider.Invoking(context.Background(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(sessionA)}})
	if err != nil {
		t.Fatalf("Invoking(sessionA) error = %v", err)
	}
	_, optsB, err := provider.Invoking(context.Background(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(sessionB)}})
	if err != nil {
		t.Fatalf("Invoking(sessionB) error = %v", err)
	}

	callTool(t, optsA, filememory.WriteToolName, `{"file_name":"profile.md","content":"alpha"}`)
	callTool(t, optsB, filememory.WriteToolName, `{"file_name":"profile.md","content":"beta"}`)

	if got := callTool(t, optsA, filememory.ReadToolName, `{"file_name":"profile.md"}`); got != "alpha" {
		t.Fatalf("sessionA read = %q", got)
	}
	if got := callTool(t, optsB, filememory.ReadToolName, `{"file_name":"profile.md"}`); got != "beta" {
		t.Fatalf("sessionB read = %q", got)
	}
}

func toolNames(opts []agent.Option) []string {
	var names []string
	for _, opt := range opts {
		if tl, ok := opt.Value().(tool.Tool); ok {
			names = append(names, tl.Name())
		}
	}
	return names
}

func collectInstructions(opts []agent.Option) string {
	var out strings.Builder
	for instruction := range agent.AllOptions(opts, agent.WithInstructions) {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(instruction)
	}
	return out.String()
}

func findTool(t *testing.T, opts []agent.Option, name string) tool.FuncTool {
	t.Helper()
	for _, opt := range opts {
		tl, ok := opt.Value().(tool.FuncTool)
		if ok && tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func callTool(t *testing.T, opts []agent.Option, name, argsJSON string) string {
	t.Helper()
	result := callToolAny(t, opts, name, argsJSON)
	out, ok := result.(string)
	if !ok {
		t.Fatalf("tool %q result type = %T, want string", name, result)
	}
	return out
}

func callToolAny(t *testing.T, opts []agent.Option, name, argsJSON string) any {
	t.Helper()
	result, err := findTool(t, opts, name).Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("tool %q error = %v", name, err)
	}
	return result
}
