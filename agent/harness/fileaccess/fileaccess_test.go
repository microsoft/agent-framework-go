// Copyright (c) Microsoft. All rights reserved.

package fileaccess_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/fileaccess"
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

func invokeProvider(p *fileaccess.Provider, opts ...agent.Option) []agent.Option {
	_, outOpts, err := p.Invoking(context.Background(), agent.InvokingContext{
		Messages: newMessages("hi"),
		Options:  opts,
	})
	if err != nil {
		panic(err)
	}
	return outOpts
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

func toolNames(opts []agent.Option) []string {
	tools := collectTools(opts)
	names := make([]string, len(tools))
	for i, tt := range tools {
		names[i] = tt.Name()
	}
	return names
}

func findTool(t *testing.T, opts []agent.Option, name string) tool.FuncTool {
	t.Helper()
	for _, tt := range collectTools(opts) {
		if tt.Name() == name {
			ft, ok := tt.(tool.FuncTool)
			if !ok {
				t.Fatalf("tool %q is not a FuncTool", name)
			}
			return ft
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func callTool(t *testing.T, opts []agent.Option, name, argsJSON string) string {
	t.Helper()
	res, err := findTool(t, opts, name).Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("tool %q call failed: %v", name, err)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", res))
}

func TestProvide_ReturnsAllToolsAndInstructions(t *testing.T) {
	p := fileaccess.New(&fileaccess.Options{RootDir: t.TempDir()})
	outOpts := invokeProvider(p, sessionOpts()...)

	names := toolNames(outOpts)
	expected := []string{
		"file_access_read_file",
		"file_access_save_file",
		"file_access_list_files",
		"file_access_list_subdirectories",
		"file_access_search_files",
		"file_access_delete_file",
	}
	for _, name := range expected {
		if !slices.Contains(names, name) {
			t.Errorf("expected tool %q, got %v", name, names)
		}
	}
	if len(names) != len(expected) {
		t.Errorf("expected %d tools, got %d: %v", len(expected), len(names), names)
	}

	var sb strings.Builder
	for inst := range agent.AllOptions(outOpts, agent.WithInstructions) {
		sb.WriteString(inst)
	}
	if !strings.Contains(sb.String(), "File Access") {
		t.Errorf("expected file-access instructions, got %q", sb.String())
	}
}

func TestReadOnly_OmitsSaveAndDelete(t *testing.T) {
	p := fileaccess.New(&fileaccess.Options{RootDir: t.TempDir(), ReadOnly: true})
	names := toolNames(invokeProvider(p, sessionOpts()...))

	if slices.Contains(names, "file_access_save_file") {
		t.Error("read-only provider must not expose file_access_save_file")
	}
	if slices.Contains(names, "file_access_delete_file") {
		t.Error("read-only provider must not expose file_access_delete_file")
	}
	for _, name := range []string{
		"file_access_read_file",
		"file_access_list_files",
		"file_access_list_subdirectories",
		"file_access_search_files",
	} {
		if !slices.Contains(names, name) {
			t.Errorf("read-only provider must still expose %q, got %v", name, names)
		}
	}
}

func TestSaveReadRoundTrip(t *testing.T) {
	root := t.TempDir()
	p := fileaccess.New(&fileaccess.Options{RootDir: root})
	outOpts := invokeProvider(p, sessionOpts()...)

	callTool(t, outOpts, "file_access_save_file", `{"path":"notes/hello.txt","content":"hello world"}`)

	// File really landed inside the root.
	got, err := os.ReadFile(filepath.Join(root, "notes", "hello.txt"))
	if err != nil {
		t.Fatalf("expected file on disk: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("on-disk content = %q, want %q", got, "hello world")
	}

	if res := callTool(t, outOpts, "file_access_read_file", `{"path":"notes/hello.txt"}`); res != "hello world" {
		t.Errorf("read = %q, want %q", res, "hello world")
	}
}

func TestListFiles_DirectChildrenOnly(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "a.txt", "a")
	mustWrite(t, root, "b.txt", "b")
	mustWrite(t, root, "sub/c.txt", "c")

	p := fileaccess.New(&fileaccess.Options{RootDir: root})
	outOpts := invokeProvider(p, sessionOpts()...)

	files := callTool(t, outOpts, "file_access_list_files", `{"path":""}`)
	if !strings.Contains(files, "a.txt") || !strings.Contains(files, "b.txt") {
		t.Errorf("expected a.txt and b.txt, got %q", files)
	}
	if strings.Contains(files, "c.txt") {
		t.Errorf("list_files must not recurse into subfolders, got %q", files)
	}

	dirs := callTool(t, outOpts, "file_access_list_subdirectories", `{"path":""}`)
	if !strings.Contains(dirs, "sub") {
		t.Errorf("expected sub directory, got %q", dirs)
	}
	if strings.Contains(dirs, "a.txt") {
		t.Errorf("list_subdirectories must not include files, got %q", dirs)
	}
}

func TestSearchFiles_RegexAcrossNestedFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "top.txt", "the quick brown fox")
	mustWrite(t, root, "nested/deep/inner.txt", "a lazy DOG barks")
	mustWrite(t, root, "nested/other.txt", "nothing to see")

	p := fileaccess.New(&fileaccess.Options{RootDir: root})
	outOpts := invokeProvider(p, sessionOpts()...)

	res := callTool(t, outOpts, "file_access_search_files", `{"pattern":"(?i)dog","path":""}`)
	if !strings.Contains(res, "nested/deep/inner.txt") {
		t.Errorf("expected nested/deep/inner.txt in results, got %q", res)
	}
	if strings.Contains(res, "top.txt") || strings.Contains(res, "other.txt") {
		t.Errorf("unexpected non-matching files in results, got %q", res)
	}
}

func TestDeleteFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "gone.txt", "bye")

	p := fileaccess.New(&fileaccess.Options{RootDir: root})
	outOpts := invokeProvider(p, sessionOpts()...)

	callTool(t, outOpts, "file_access_delete_file", `{"path":"gone.txt"}`)
	if _, err := os.Stat(filepath.Join(root, "gone.txt")); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted, stat err = %v", err)
	}
}

func TestPathEscape_Rejected(t *testing.T) {
	root := t.TempDir()
	// Plant a file outside the root to prove it stays untouched.
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	p := fileaccess.New(&fileaccess.Options{RootDir: root})
	outOpts := invokeProvider(p, sessionOpts()...)

	// Reads that escape the root must error, not return the outside file.
	if _, err := findTool(t, outOpts, "file_access_read_file").
		Call(context.Background(), `{"path":"../outside.txt"}`); err == nil {
		t.Error("expected read of ../outside.txt to be rejected")
	}

	// Writes that escape the root must error and not create the file.
	if _, err := findTool(t, outOpts, "file_access_save_file").
		Call(context.Background(), `{"path":"../escaped.txt","content":"x"}`); err == nil {
		t.Error("expected save of ../escaped.txt to be rejected")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escaped.txt")); !os.IsNotExist(err) {
		t.Errorf("escaping write must not create a file outside root, stat err = %v", err)
	}

	// Absolute paths are rejected too.
	if _, err := findTool(t, outOpts, "file_access_read_file").
		Call(context.Background(), `{"path":"`+jsonEscape(outside)+`"}`); err == nil {
		t.Error("expected read of absolute path to be rejected")
	}
}

func mustWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jsonEscape(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}
