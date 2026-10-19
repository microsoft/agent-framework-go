// Copyright (c) Microsoft. All rights reserved.

package filememory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/filestore"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func providerTools(t *testing.T, p *Provider, session *agent.Session) map[string]tool.FuncTool {
	t.Helper()
	_, opts, err := p.Invoking(t.Context(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(session)}})
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]tool.FuncTool)
	for value := range agent.AllOptions(opts, agent.WithTool) {
		fn, ok := value.(tool.FuncTool)
		if !ok {
			t.Fatalf("provided tool has type %T, want tool.FuncTool", value)
		}
		tools[fn.Name()] = fn
	}
	return tools
}

func newMemoryFixture(t *testing.T, cfg Config) (*Provider, *memoryStore, *agent.Session, map[string]tool.FuncTool) {
	t.Helper()
	store := &memoryStore{files: make(map[string]string)}
	session := &agent.Session{}
	p := newProvider(store, cfg)
	return p, store, session, providerTools(t, p, session)
}

func callMemoryTool(t *testing.T, tools map[string]tool.FuncTool, name, args string) any {
	t.Helper()
	fn, found := tools[name]
	if !found {
		t.Fatalf("tool %q was not registered", name)
	}
	result, err := fn.Call(t.Context(), args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func resultJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readStore(t *testing.T, store filestore.Store, name string) string {
	t.Helper()
	content, err := store.Read(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func writeMemory(t *testing.T, tools map[string]tool.FuncTool, name, content, description string) any {
	t.Helper()
	args, err := json.Marshal(map[string]string{"fileName": name, "content": content, "description": description})
	if err != nil {
		t.Fatal(err)
	}
	return callMemoryTool(t, tools, WriteToolName, string(args))
}

func TestNew_InvalidRoot(t *testing.T) {
	for _, root := range []string{"", " \t\n"} {
		p, err := New(root, Config{})
		if p != nil || !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("New(%q) = %v, %v, want nil and fs.ErrInvalid", root, p, err)
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := New(file, Config{}); p != nil || err == nil {
		t.Fatalf("New(file) = %v, %v, want an error", p, err)
	}
}

func TestProvider_PersistsAfterClose(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agent-memory")
	p, err := New(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	tools := providerTools(t, p, &agent.Session{})
	writeMemory(t, tools, "notes.md", "remembered content", "summary")
	for range 2 {
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for name, args := range map[string]string{
		WriteToolName:        `{"fileName":"notes.md","content":"changed"}`,
		ReadFileToolName:     `{"fileName":"notes.md"}`,
		DeleteFileToolName:   `{"fileName":"notes.md"}`,
		LsToolName:           `{}`,
		GrepToolName:         `{"regexPattern":"remembered"}`,
		ReplaceToolName:      `{"fileName":"notes.md","oldString":"remembered","newString":"changed"}`,
		ReplaceLinesToolName: `{"fileName":"notes.md","edits":[{"line_number":1,"new_line":"changed"}]}`,
	} {
		if _, err := tools[name].Call(t.Context(), args); !errors.Is(err, fs.ErrClosed) {
			t.Fatalf("%s after Close returned %v, want fs.ErrClosed", name, err)
		}
	}
	if _, _, err := p.Invoking(t.Context(), agent.InvokingContext{}); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("invoking after Close returned %v, want fs.ErrClosed", err)
	}
	reopened, err := New(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	session := &agent.Session{}
	reopenedTools := providerTools(t, reopened, session)
	if got := callMemoryTool(t, reopenedTools, ReadFileToolName, `{"fileName":"notes.md"}`); got != "remembered content" {
		t.Fatalf("reopened memory = %v", got)
	}
	messages, _, err := reopened.Invoking(t.Context(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(session)}})
	if err != nil || len(messages) != 1 || !strings.Contains(messages[0].Contents.Text(), "- **notes.md**: summary") {
		t.Fatalf("reopened index = %v, %v", messages, err)
	}
}

func TestProvider_RuntimeCleanup(t *testing.T) {
	closed := make(chan struct{})
	var calls atomic.Int32
	func() {
		newProvider(&cleanupMemoryStore{
			memoryStore: &memoryStore{files: make(map[string]string)},
			close: func() error {
				calls.Add(1)
				close(closed)
				return nil
			},
		}, Config{})
	}()
	waitForFileMemoryCleanup(t, closed)
	if calls.Load() != 1 {
		t.Fatalf("runtime cleanup closed storage %d times, want 1", calls.Load())
	}
}

func TestProvider_CloseStopsRuntimeCleanup(t *testing.T) {
	released := make(chan struct{})
	var calls atomic.Int32
	func() {
		p := newProvider(&cleanupMemoryStore{
			memoryStore: &memoryStore{files: make(map[string]string)},
			close:       func() error { calls.Add(1); return nil },
		}, Config{})
		runtime.AddCleanup(p, func(done chan struct{}) { close(done) }, released)
		for range 2 {
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}()
	waitForFileMemoryCleanup(t, released)
	if calls.Load() != 2 {
		t.Fatalf("runtime cleanup repeated explicit Close: %d calls", calls.Load())
	}
}

func TestProvider_FailedCloseRetainsRuntimeCleanup(t *testing.T) {
	closed := make(chan struct{})
	var calls atomic.Int32
	failure := errors.New("close failed")
	func() {
		p := newProvider(&cleanupMemoryStore{
			memoryStore: &memoryStore{files: make(map[string]string)},
			close: func() error {
				if calls.Add(1) == 1 {
					return failure
				}
				close(closed)
				return nil
			},
		}, Config{})
		if err := p.Close(); !errors.Is(err, failure) {
			t.Fatalf("Close error = %v, want %v", err, failure)
		}
	}()
	waitForFileMemoryCleanup(t, closed)
	if calls.Load() != 2 {
		t.Fatalf("failed Close was not retried by runtime cleanup: %d calls", calls.Load())
	}
}

func TestProvider_ToolsRetainStorage(t *testing.T) {
	closed := make(chan struct{})
	tools := func() map[string]tool.FuncTool {
		p := newProvider(&cleanupMemoryStore{
			memoryStore: &memoryStore{files: map[string]string{"notes.md": "retained"}},
			close:       func() error { close(closed); return nil },
		}, Config{})
		return providerTools(t, p, nil)
	}()
	runtime.GC()
	select {
	case <-closed:
		t.Fatal("runtime cleanup closed storage while tools still referenced it")
	default:
	}
	if got := callMemoryTool(t, tools, ReadFileToolName, `{"fileName":"notes.md"}`); got != "retained" {
		t.Fatalf("retained tool result = %v", got)
	}
	runtime.KeepAlive(tools)
	waitForFileMemoryCleanup(t, closed)
}

func waitForFileMemoryCleanup(t *testing.T, done <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		runtime.GC()
		select {
		case <-done:
			return
		case <-ctx.Done():
			t.Fatal("file memory runtime cleanup did not finish")
		default:
			runtime.Gosched()
		}
	}
}

type cleanupMemoryStore struct {
	*memoryStore
	close func() error
}

func (s *cleanupMemoryStore) Close() error { return s.close() }

func TestProvider_Defaults(t *testing.T) {
	p, _, session, tools := newMemoryFixture(t, Config{})
	want := []string{
		WriteToolName, ReadFileToolName, DeleteFileToolName, LsToolName,
		GrepToolName, ReplaceToolName, ReplaceLinesToolName,
	}
	if len(tools) != len(want) {
		t.Fatalf("provided %d tools, want %d", len(tools), len(want))
	}
	for _, name := range want {
		if tools[name] == nil {
			t.Errorf("missing %s", name)
		}
	}
	var state State
	found, err := session.Get(p.StateKeys()[0], &state)
	if err != nil || !found || state.WorkingFolder != "" {
		t.Fatalf("default state = %+v, found = %v, error = %v", state, found, err)
	}
	if got := resultJSON(t, callMemoryTool(t, tools, LsToolName, `{}`)); got != "[]" {
		t.Fatalf("empty listing = %s, want []", got)
	}
	if err := p.Invoked(t.Context(), agent.InvokedContext{Err: errors.New("run failed")}); err != nil {
		t.Fatal(err)
	}
	keys := p.StateKeys()
	keys[0] = "changed"
	if p.StateKeys()[0] == "changed" {
		t.Fatal("StateKeys exposed mutable provider state")
	}
}

func TestProvider_Instructions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value *string
		want  string
	}{
		{name: "default", want: defaultInstructions},
		{name: "custom", value: new("Track engineering memories."), want: "Track engineering memories."},
		{name: "empty", value: new("")},
		{name: "whitespace", value: new(" \t\r\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProvider(&memoryStore{files: make(map[string]string)}, Config{Instructions: tc.value})
			input := message.NewText("request")
			opts := []agent.Option{agent.WithInstructions("caller instructions")}
			messages, out, err := p.Invoking(t.Context(), agent.InvokingContext{Messages: []*message.Message{input}, Options: opts})
			if err != nil {
				t.Fatal(err)
			}
			instructions := slices.Collect(agent.AllOptions(out, agent.WithInstructions))
			want := []string{"caller instructions"}
			if tc.want != "" {
				want = append(want, tc.want)
			}
			if !slices.Equal(instructions, want) {
				t.Fatalf("instructions = %q", instructions)
			}
			if len(opts) != 1 || len(messages) != 1 || messages[0] != input {
				t.Fatal("provider changed the caller's input")
			}
			if tc.want == "" {
				_, out, err := p.Invoking(t.Context(), agent.InvokingContext{})
				if err != nil {
					t.Fatal(err)
				}
				if instructions := slices.Collect(agent.AllOptions(out, agent.WithInstructions)); len(instructions) != 0 {
					t.Fatalf("blank provider instructions added options: %q", instructions)
				}
			}
		})
	}
}

func TestProvider_ToolSchemas(t *testing.T) {
	_, _, _, tools := newMemoryFixture(t, Config{})
	for _, tc := range []struct {
		name     string
		required []string
		optional []string
	}{
		{name: WriteToolName, required: []string{"fileName", "content"}, optional: []string{"description"}},
		{name: ReadFileToolName, required: []string{"fileName"}},
		{name: DeleteFileToolName, required: []string{"fileName"}},
		{name: LsToolName, optional: []string{"globPattern"}},
		{name: GrepToolName, required: []string{"regexPattern"}, optional: []string{"globPattern"}},
		{name: ReplaceToolName, required: []string{"fileName", "oldString", "newString"}, optional: []string{"replaceAll"}},
		{name: ReplaceLinesToolName, required: []string{"fileName", "edits"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal([]byte(resultJSON(t, tools[tc.name].Schema())), &schema); err != nil {
				t.Fatal(err)
			}
			if len(schema.Properties) != len(tc.required)+len(tc.optional) {
				t.Fatalf("unexpected schema properties: %v", schema.Properties)
			}
			for _, name := range tc.required {
				if schema.Properties[name] == nil || !slices.Contains(schema.Required, name) {
					t.Errorf("%q must be a required named argument", name)
				}
			}
			for _, name := range tc.optional {
				if schema.Properties[name] == nil || slices.Contains(schema.Required, name) {
					t.Errorf("%q must be optional", name)
				}
			}
			if schema.Properties["Arg0"] != nil {
				t.Fatal("tool arguments must not have an Arg0 wrapper")
			}
		})
	}
}

func TestProvider_WriteDescriptionsAndIndex(t *testing.T) {
	p, store, session, tools := newMemoryFixture(t, Config{})
	if got := writeMemory(t, tools, "notes.md", "content", "summary"); got != "File 'notes.md' written with description." {
		t.Fatalf("write result = %v", got)
	}
	if readStore(t, store, "notes.md") != "content" || readStore(t, store, "notes.md_description.md") != "summary" {
		t.Fatal("memory or description content did not persist")
	}
	wantIndex := "# Memory Index\n\n- **notes.md**: summary\n"
	if got := readStore(t, store, "memories.md"); got != wantIndex {
		t.Fatalf("memory index = %q, want %q", got, wantIndex)
	}
	messages, _, err := p.Invoking(t.Context(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(session)}})
	if err != nil || len(messages) != 1 {
		t.Fatalf("index injection returned %d messages, %v", len(messages), err)
	}
	if got := messages[0].Contents.Text(); got != memoryIndexIntroduction+wantIndex {
		t.Fatalf("index message = %q", got)
	}
	if messages[0].Source.Type != agent.SourceTypeContextProvider || messages[0].Source.ID != "FileMemoryProvider" {
		t.Fatalf("index message source = %+v", messages[0].Source)
	}
	list := callMemoryTool(t, tools, LsToolName, `{}`)
	wantList := `[{"name":"notes.md","type":"file","description":"summary"}]`
	if got := resultJSON(t, list); got != wantList {
		t.Fatalf("listing = %s, want %s", got, wantList)
	}
}

func TestProvider_OverwriteRemovesStaleDescription(t *testing.T) {
	for _, args := range []string{
		`{"fileName":"notes.md","content":"changed"}`,
		`{"fileName":"notes.md","content":"changed","description":""}`,
		`{"fileName":"notes.md","content":"changed","description":" \t\n"}`,
	} {
		t.Run(args, func(t *testing.T) {
			_, store, _, tools := newMemoryFixture(t, Config{})
			writeMemory(t, tools, "notes.md", "old", "summary")
			if got := callMemoryTool(t, tools, WriteToolName, args); got != "File 'notes.md' written." {
				t.Fatalf("write result = %v", got)
			}
			if _, err := store.Read(t.Context(), "notes.md_description.md"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("stale description remains: %v", err)
			}
			if got := readStore(t, store, "memories.md"); got != "# Memory Index\n\n- **notes.md**\n" {
				t.Fatalf("index = %q", got)
			}
		})
	}
}

func TestProvider_DescriptionsDoNotCollide(t *testing.T) {
	_, store, _, tools := newMemoryFixture(t, Config{})
	writeMemory(t, tools, "notes.md", "markdown", "Markdown summary")
	writeMemory(t, tools, "notes.txt", "text", "Text summary")
	if readStore(t, store, "notes.md_description.md") != "Markdown summary" || readStore(t, store, "notes.txt_description.md") != "Text summary" {
		t.Fatal("descriptions for different extensions collided")
	}
	callMemoryTool(t, tools, DeleteFileToolName, `{"fileName":"notes.md"}`)
	if got := readStore(t, store, "notes.txt_description.md"); got != "Text summary" {
		t.Fatalf("deleting one memory changed another description: %q", got)
	}
}

func TestProvider_CaseSensitiveDescriptions(t *testing.T) {
	_, store, _, tools := newMemoryFixture(t, Config{})
	writeMemory(t, tools, "Notes.md", "upper", "Uppercase summary")
	writeMemory(t, tools, "notes.md", "lower", "Lowercase summary")
	want := `[{"name":"Notes.md","type":"file","description":"Uppercase summary"},{"name":"notes.md","type":"file","description":"Lowercase summary"}]`
	if got := resultJSON(t, callMemoryTool(t, tools, LsToolName, `{}`)); got != want {
		t.Fatalf("listing = %s, want %s", got, want)
	}
	wantIndex := "# Memory Index\n\n- **Notes.md**: Uppercase summary\n- **notes.md**: Lowercase summary\n"
	if got := readStore(t, store, "memories.md"); got != wantIndex {
		t.Fatalf("index = %q, want %q", got, wantIndex)
	}
	callMemoryTool(t, tools, DeleteFileToolName, `{"fileName":"Notes.md"}`)
	want = `[{"name":"notes.md","type":"file","description":"Lowercase summary"}]`
	if got := resultJSON(t, callMemoryTool(t, tools, LsToolName, `{}`)); got != want {
		t.Fatalf("listing after deletion = %s, want %s", got, want)
	}
}

func TestProvider_ReadAndDelete(t *testing.T) {
	_, store, _, tools := newMemoryFixture(t, Config{})
	writeMemory(t, tools, "empty.md", "", "")
	if got := callMemoryTool(t, tools, ReadFileToolName, `{"fileName":"empty.md"}`); got != "" {
		t.Fatalf("existing empty file = %v", got)
	}
	if got := callMemoryTool(t, tools, ReadFileToolName, `{"fileName":"missing.md"}`); got != "File 'missing.md' not found." {
		t.Fatalf("missing read = %v", got)
	}
	writeMemory(t, tools, "notes.md", "content", "summary")
	if got := callMemoryTool(t, tools, DeleteFileToolName, `{"fileName":"notes.md"}`); got != "File 'notes.md' deleted." {
		t.Fatalf("delete result = %v", got)
	}
	for _, name := range []string{"notes.md", "notes.md_description.md"} {
		if _, err := store.Read(t.Context(), name); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s was not deleted: %v", name, err)
		}
	}
	if got := callMemoryTool(t, tools, DeleteFileToolName, `{"fileName":"notes.md"}`); got != "File 'notes.md' not found." {
		t.Fatalf("missing delete = %v", got)
	}
	if got := readStore(t, store, "memories.md"); got != "# Memory Index\n\n- **empty.md**\n" {
		t.Fatalf("index after delete = %q", got)
	}
}

func TestProvider_IndexOrderingAndLimit(t *testing.T) {
	store := &memoryStore{files: make(map[string]string)}
	for i := 0; i < 55; i++ {
		if err := store.Write(t.Context(), fmt.Sprintf("file%02d.md", i), "content"); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"Zebra.md": "last", "alpha.md": "first", "ignored_description.md": "internal", "memories.md": "old"} {
		if err := store.Write(t.Context(), name, content); err != nil {
			t.Fatal(err)
		}
	}
	p := newProvider(store, Config{})
	tools := providerTools(t, p, &agent.Session{})
	writeMemory(t, tools, "file00.md", "changed", "")
	index := readStore(t, store, "memories.md")
	lines := strings.Split(strings.TrimSuffix(index, "\n"), "\n")
	if len(lines) != 52 || lines[2] != "- **alpha.md**" || lines[51] != "- **file48.md**" {
		t.Fatalf("index must contain the first 50 sorted user files: %q", index)
	}
	if strings.Contains(index, "description.md") || strings.Contains(index, "**memories.md**") {
		t.Fatal("index contains internal files")
	}
	if got := callMemoryTool(t, tools, ReadFileToolName, `{"fileName":"file54.md"}`); got != "content" {
		t.Fatalf("index cap must not remove memory files: %v", got)
	}
}

func TestProvider_ListAndGrepHideInternalFiles(t *testing.T) {
	_, store, _, tools := newMemoryFixture(t, Config{})
	writeMemory(t, tools, "notes.MD", "first\r\nMATCH\r\nlast", "MATCH summary")
	writeMemory(t, tools, "other.txt", "match", "")
	if err := store.Write(t.Context(), "MANUAL_DESCRIPTION.MD", "match"); err != nil {
		t.Fatal(err)
	}
	got := resultJSON(t, callMemoryTool(t, tools, LsToolName, `{"globPattern":"*.md"}`))
	if got != `[{"name":"notes.MD","type":"file","description":"MATCH summary"}]` {
		t.Fatalf("filtered listing = %s", got)
	}
	search := resultJSON(t, callMemoryTool(t, tools, GrepToolName, `{"regexPattern":"match","globPattern":"*.MD"}`))
	want := `[{"fileName":"notes.MD","snippet":"first\r\nMATCH\r\nlast","matchingLines":[{"lineNumber":2,"line":"MATCH\r\n"}]}]`
	if search != want {
		t.Fatalf("grep result = %s, want %s", search, want)
	}
	if got := resultJSON(t, callMemoryTool(t, tools, GrepToolName, `{"regexPattern":"absent"}`)); got != "[]" {
		t.Fatalf("no-match grep = %s", got)
	}
}

func TestProvider_FilenamePatternSemantics(t *testing.T) {
	_, _, _, tools := newMemoryFixture(t, Config{})
	for _, name := range []string{"notes[1].md", "notes1.md", "notes?.md", "\u03c2.md", "README"} {
		writeMemory(t, tools, name, "matching content", "")
	}
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{pattern: "notes[1].md", want: []string{"notes[1].md"}},
		{pattern: "notes?.md", want: []string{"notes?.md"}},
		{pattern: "\u03a3.md", want: []string{"\u03c2.md"}},
		{pattern: "**/notes1.md", want: []string{"notes1.md"}},
		{pattern: "*.*", want: []string{"README", "notes1.md", "notes?.md", "notes[1].md", "\u03c2.md"}},
		{pattern: "["},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			listArgs := resultJSON(t, map[string]string{"globPattern": tc.pattern})
			var listing []ListEntry
			if err := json.Unmarshal([]byte(resultJSON(t, callMemoryTool(t, tools, LsToolName, listArgs))), &listing); err != nil {
				t.Fatal(err)
			}
			var listedNames []string
			for _, entry := range listing {
				listedNames = append(listedNames, entry.Name)
			}
			if !slices.Equal(listedNames, tc.want) {
				t.Fatalf("listed names = %q, want %q", listedNames, tc.want)
			}
			grepArgs := resultJSON(t, map[string]string{"regexPattern": "matching", "globPattern": tc.pattern})
			var matches []struct {
				FileName string `json:"fileName"`
			}
			if err := json.Unmarshal([]byte(resultJSON(t, callMemoryTool(t, tools, GrepToolName, grepArgs))), &matches); err != nil {
				t.Fatal(err)
			}
			var matchedNames []string
			for _, match := range matches {
				matchedNames = append(matchedNames, match.FileName)
			}
			if !slices.Equal(matchedNames, tc.want) {
				t.Fatalf("matched names = %q, want %q", matchedNames, tc.want)
			}
		})
	}
}

func TestProvider_RejectsInvalidAndReservedFilenames(t *testing.T) {
	_, store, _, tools := newMemoryFixture(t, Config{})
	for _, name := range []string{
		"", " \t", "../outside", "./notes.md", "/absolute.md", `C:\notes.md`,
		"nested/notes.md", `nested\notes.md`, "nested/../notes.md", `nested\..\notes.md`,
		"nested/./notes.md", `.\notes.md`, "memories.md", "MEMORIES.MD",
		"notes.md_description.md", "NOTES_DESCRIPTION.MD",
	} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			for _, toolName := range []string{WriteToolName, ReadFileToolName, DeleteFileToolName, ReplaceToolName, ReplaceLinesToolName} {
				args := map[string]any{"fileName": name}
				switch toolName {
				case WriteToolName:
					args["content"] = "content"
				case ReplaceToolName:
					args["oldString"], args["newString"] = "old", "new"
				case ReplaceLinesToolName:
					args["edits"] = []map[string]any{{"line_number": 1, "new_line": "new"}}
				}
				_, err := tools[toolName].Call(t.Context(), resultJSON(t, args))
				if !errors.Is(err, fs.ErrInvalid) {
					t.Fatalf("%s returned %v, want fs.ErrInvalid", toolName, err)
				}
			}
		})
	}
	names, err := store.ListChildren(t.Context(), "")
	if err != nil || len(names) != 0 {
		t.Fatalf("invalid calls wrote files: %v, %v", names, err)
	}
	writeMemory(t, tools, "notes..md", "allowed", "")
}

func TestProvider_NormalizesFlatFilenames(t *testing.T) {
	_, _, _, tools := newMemoryFixture(t, Config{})
	for _, name := range []string{"notes.md/", "notes.md//", `notes.md\`} {
		writeMemory(t, tools, name, "content", "")
		if got := callMemoryTool(t, tools, ReadFileToolName, `{"fileName":"notes.md"}`); got != "content" {
			t.Fatalf("normalized filename %q read = %v", name, got)
		}
	}
}

func TestProvider_ReplaceTools(t *testing.T) {
	_, store, _, tools := newMemoryFixture(t, Config{})
	writeMemory(t, tools, "notes.md", "one one\r\nsecond\rlast", "summary")
	if _, err := tools[ReplaceToolName].Call(t.Context(), `{"fileName":"notes.md","oldString":"one","newString":"two"}`); err == nil {
		t.Fatal("ambiguous replacement must fail")
	}
	if got := readStore(t, store, "notes.md"); got != "one one\r\nsecond\rlast" {
		t.Fatalf("failed replacement changed file: %q", got)
	}
	if got := callMemoryTool(t, tools, ReplaceToolName, `{"fileName":"notes.md","oldString":"one","newString":"two","replaceAll":true}`); got != "Replaced 2 occurrence(s) in 'notes.md'." {
		t.Fatalf("replace result = %v", got)
	}
	if got := callMemoryTool(t, tools, ReplaceLinesToolName, `{"fileName":"notes.md","edits":[{"line_number":2,"new_line":"changed\r","expected_line":"second"}]}`); got != "Replaced 1 line(s) in 'notes.md'." {
		t.Fatalf("line replacement result = %v", got)
	}
	if got := readStore(t, store, "notes.md"); got != "two two\r\nchanged\rlast" {
		t.Fatalf("edited content = %q", got)
	}
	for _, edits := range []any{
		[]any{},
		[]map[string]any{{"line_number": 0, "new_line": "bad"}},
		[]map[string]any{{"line_number": 4, "new_line": "bad"}},
		[]map[string]any{{"line_number": 1, "new_line": "bad"}, {"line_number": 1, "new_line": "bad"}},
		[]map[string]any{{"line_number": 1, "new_line": "bad"}, {"line_number": 2, "new_line": "bad", "expected_line": "stale"}},
	} {
		args := resultJSON(t, map[string]any{"fileName": "notes.md", "edits": edits})
		if _, err := tools[ReplaceLinesToolName].Call(t.Context(), args); err == nil {
			t.Fatalf("invalid edits accepted: %s", args)
		}
		if got := readStore(t, store, "notes.md"); got != "two two\r\nchanged\rlast" {
			t.Fatalf("invalid edits changed file: %q", got)
		}
	}
	if got := readStore(t, store, "notes.md_description.md"); got != "summary" {
		t.Fatalf("editing changed the description: %q", got)
	}
	for _, name := range []string{ReplaceToolName, ReplaceLinesToolName} {
		args := `{"fileName":"missing.md","oldString":"x","newString":"y"}`
		if name == ReplaceLinesToolName {
			args = `{"fileName":"missing.md","edits":[{"line_number":1,"new_line":"new"}]}`
		}
		if got := callMemoryTool(t, tools, name, args); got != "File 'missing.md' not found." {
			t.Fatalf("%s missing result = %v", name, got)
		}
	}
}

func TestProvider_StatePersistenceAndSessionBinding(t *testing.T) {
	store := &memoryStore{files: make(map[string]string)}
	var initializations atomic.Int32
	p := newProvider(store, Config{StateInitializer: func(session *agent.Session) State {
		initializations.Add(1)
		return State{WorkingFolder: "sessions/" + session.ServiceID()}
	}})
	a, b := &agent.Session{}, &agent.Session{}
	a.SetServiceID("a")
	b.SetServiceID("b")
	toolsA, toolsB := providerTools(t, p, a), providerTools(t, p, b)
	writeMemory(t, toolsA, "notes.md", "A", "A summary")
	writeMemory(t, toolsB, "notes.md", "B", "B summary")
	if got := callMemoryTool(t, toolsA, ReadFileToolName, `{"fileName":"notes.md"}`); got != "A" {
		t.Fatalf("session A read = %v", got)
	}
	if got := callMemoryTool(t, toolsB, ReadFileToolName, `{"fileName":"notes.md"}`); got != "B" {
		t.Fatalf("session B read = %v", got)
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredTools := providerTools(t, p, &restored)
	if got := callMemoryTool(t, restoredTools, ReadFileToolName, `{"fileName":"notes.md"}`); got != "A" {
		t.Fatalf("restored session read = %v", got)
	}
	if got := initializations.Load(); got != 2 {
		t.Fatalf("initializer called %d times, want once per new session", got)
	}
	var state State
	if found, err := restored.Get(p.StateKeys()[0], &state); err != nil || !found || state.WorkingFolder != "sessions/a" {
		t.Fatalf("restored state = %+v, found = %v, error = %v", state, found, err)
	}
}

func TestProvider_DefaultRootSharesMemories(t *testing.T) {
	p, _, _, toolsA := newMemoryFixture(t, Config{})
	writeMemory(t, toolsA, "notes.md", "shared", "")
	toolsB := providerTools(t, p, &agent.Session{})
	if got := callMemoryTool(t, toolsB, ReadFileToolName, `{"fileName":"notes.md"}`); got != "shared" {
		t.Fatalf("default working folder should share persistent memories: %v", got)
	}
}

func TestProvider_ToolsCaptureWorkingFolder(t *testing.T) {
	p, store, session, tools := newMemoryFixture(t, Config{StateInitializer: func(*agent.Session) State {
		return State{WorkingFolder: "original"}
	}})
	writeMemory(t, tools, "notes.md", "first\nsecond\n", "")
	if err := store.Write(t.Context(), "changed/other.md", "other memory"); err != nil {
		t.Fatal(err)
	}
	session.Set(p.StateKeys()[0], State{WorkingFolder: "changed"})
	if got := callMemoryTool(t, tools, ReadFileToolName, `{"fileName":"notes.md"}`); got != "first\nsecond\n" {
		t.Fatalf("read used a changed working folder: %v", got)
	}
	if got := resultJSON(t, callMemoryTool(t, tools, LsToolName, `{}`)); got != `[{"name":"notes.md","type":"file"}]` {
		t.Fatalf("list used a changed working folder: %s", got)
	}
	results := callMemoryTool(t, tools, GrepToolName, `{"regexPattern":"first"}`).([]searchResult)
	if len(results) != 1 || results[0].FileName != "notes.md" {
		t.Fatalf("grep used a changed working folder: %+v", results)
	}
	callMemoryTool(t, tools, ReplaceToolName, `{"fileName":"notes.md","oldString":"first","newString":"updated"}`)
	callMemoryTool(t, tools, ReplaceLinesToolName, `{"fileName":"notes.md","edits":[{"line_number":2,"new_line":"last\n"}]}`)
	if got := readStore(t, store, "original/notes.md"); got != "updated\nlast\n" {
		t.Fatalf("replacement tools used a changed working folder: %q", got)
	}
	writeMemory(t, tools, "new.md", "new memory", "")
	if got := readStore(t, store, "original/new.md"); got != "new memory" {
		t.Fatalf("write used a changed working folder: %q", got)
	}
	callMemoryTool(t, tools, DeleteFileToolName, `{"fileName":"notes.md"}`)
	if _, err := store.Read(t.Context(), "original/notes.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("delete used a changed working folder: %v", err)
	}
	nextTools := providerTools(t, p, session)
	if got := callMemoryTool(t, nextTools, ReadFileToolName, `{"fileName":"other.md"}`); got != "other memory" {
		t.Fatalf("next invocation did not use the changed working folder: %v", got)
	}
}

func TestProvider_WorkingFolderCreatedOnInvocation(t *testing.T) {
	root := t.TempDir()
	p, err := New(root, Config{StateInitializer: func(*agent.Session) State {
		return State{WorkingFolder: `users\alice/`}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	tools := providerTools(t, p, &agent.Session{})
	if info, err := os.Stat(filepath.Join(root, "users", "alice")); err != nil || !info.IsDir() {
		t.Fatalf("working folder was not created: %v", err)
	}
	writeMemory(t, tools, "notes.md", "content", "summary")
	got, err := os.ReadFile(filepath.Join(root, "users", "alice", "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "content" {
		t.Fatalf("wrong working folder: %q", got)
	}
}

func TestProvider_RejectsInvalidWorkingFolder(t *testing.T) {
	store := &memoryStore{files: make(map[string]string)}
	p := newProvider(store, Config{StateInitializer: func(*agent.Session) State {
		return State{WorkingFolder: "../outside"}
	}})
	session := &agent.Session{}
	if _, _, err := p.Invoking(t.Context(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(session)}}); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("error = %v, want fs.ErrInvalid", err)
	}
	var state State
	if found, err := session.Get(p.StateKeys()[0], &state); err != nil || found {
		t.Fatalf("invalid initializer state was persisted: found = %v, error = %v", found, err)
	}
}

func TestProvider_WithoutSession(t *testing.T) {
	store := &memoryStore{files: make(map[string]string)}
	var sawNil bool
	var initializations int
	p := newProvider(store, Config{StateInitializer: func(session *agent.Session) State {
		initializations++
		sawNil = session == nil
		return State{WorkingFolder: "stateless"}
	}})
	tools := providerTools(t, p, nil)
	writeMemory(t, tools, "notes.md", "content", "")
	if !sawNil || readStore(t, store, "stateless/notes.md") != "content" {
		t.Fatal("nil-session initializer was not used")
	}
	if initializations != 1 {
		t.Fatalf("initializer called %d times, want once per invocation", initializations)
	}
}

func TestProvider_ConcurrentWritesAndDeletes(t *testing.T) {
	_, store, _, tools := newMemoryFixture(t, Config{})
	for i := 0; i < 20; i++ {
		writeMemory(t, tools, fmt.Sprintf("old%02d.md", i), "old", "")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			args := fmt.Sprintf(`{"fileName":"new%02d.md","content":"content","description":"summary"}`, i)
			_, err := tools[WriteToolName].Call(t.Context(), args)
			failures <- err
		})
		wg.Go(func() {
			args := fmt.Sprintf(`{"fileName":"old%02d.md"}`, i)
			_, err := tools[DeleteFileToolName].Call(t.Context(), args)
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Error(err)
		}
	}
	index := readStore(t, store, "memories.md")
	if strings.Count(index, "- **") != 20 || strings.Contains(index, "old") {
		t.Fatalf("concurrent operations lost index updates: %q", index)
	}
	for i := 0; i < 20; i++ {
		if !strings.Contains(index, fmt.Sprintf("- **new%02d.md**: summary\n", i)) {
			t.Errorf("new%02d.md is absent from the index", i)
		}
	}
}

func TestProvider_BackendErrorsArePropagated(t *testing.T) {
	failure := errors.New("storage failed")
	for _, operation := range []string{"read", "create directory"} {
		t.Run("invoking "+operation, func(t *testing.T) {
			store := &failingMemoryStore{Store: &memoryStore{files: make(map[string]string)}, operation: operation, failure: failure}
			p := newProvider(store, Config{StateInitializer: func(*agent.Session) State { return State{WorkingFolder: "work"} }})
			if _, _, err := p.Invoking(t.Context(), agent.InvokingContext{Options: []agent.Option{agent.WithSession(&agent.Session{})}}); !errors.Is(err, failure) {
				t.Fatalf("error = %v, want original storage error", err)
			}
		})
	}
	for _, tc := range []struct {
		name      string
		tool      string
		operation string
		path      string
		args      string
	}{
		{name: "write", tool: WriteToolName, operation: "write", path: "notes.md", args: `{"fileName":"notes.md","content":"text"}`},
		{name: "description write", tool: WriteToolName, operation: "write", path: "notes.md_description.md", args: `{"fileName":"notes.md","content":"text","description":"summary"}`},
		{name: "description delete", tool: WriteToolName, operation: "delete", path: "notes.md_description.md", args: `{"fileName":"notes.md","content":"text"}`},
		{name: "index write", tool: WriteToolName, operation: "write", path: "memories.md", args: `{"fileName":"notes.md","content":"text"}`},
		{name: "index list", tool: WriteToolName, operation: "list", args: `{"fileName":"notes.md","content":"text"}`},
		{name: "read", tool: ReadFileToolName, operation: "read", path: "notes.md", args: `{"fileName":"notes.md"}`},
		{name: "delete", tool: DeleteFileToolName, operation: "delete", path: "notes.md", args: `{"fileName":"notes.md"}`},
		{name: "list", tool: LsToolName, operation: "list", args: `{}`},
		{name: "grep", tool: GrepToolName, operation: "read", path: "notes.md", args: `{"regexPattern":"text"}`},
		{name: "replace read", tool: ReplaceToolName, operation: "read", path: "notes.md", args: `{"fileName":"notes.md","oldString":"text","newString":"changed"}`},
		{name: "replace write", tool: ReplaceToolName, operation: "write", path: "notes.md", args: `{"fileName":"notes.md","oldString":"text","newString":"changed"}`},
		{name: "line edit write", tool: ReplaceLinesToolName, operation: "write", path: "notes.md", args: `{"fileName":"notes.md","edits":[{"line_number":1,"new_line":"changed"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &memoryStore{files: map[string]string{"notes.md": "text"}}
			store := &failingMemoryStore{Store: base}
			p := newProvider(store, Config{})
			tools := providerTools(t, p, &agent.Session{})
			store.operation, store.path, store.failure = tc.operation, tc.path, failure
			if result, err := tools[tc.tool].Call(t.Context(), tc.args); !errors.Is(err, failure) || result != nil {
				t.Fatalf("result = %v, error = %v, want original storage error", result, err)
			}
		})
	}
}

func TestProvider_CancelledOperations(t *testing.T) {
	p, store, session, tools := newMemoryFixture(t, Config{})
	writeMemory(t, tools, "notes.md", "original", "summary")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for name, args := range map[string]string{
		WriteToolName:        `{"fileName":"notes.md","content":"changed"}`,
		ReadFileToolName:     `{"fileName":"notes.md"}`,
		DeleteFileToolName:   `{"fileName":"notes.md"}`,
		LsToolName:           `{}`,
		GrepToolName:         `{"regexPattern":"original"}`,
		ReplaceToolName:      `{"fileName":"notes.md","oldString":"original","newString":"changed"}`,
		ReplaceLinesToolName: `{"fileName":"notes.md","edits":[{"line_number":1,"new_line":"changed"}]}`,
	} {
		if _, err := tools[name].Call(ctx, args); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s error = %v, want context.Canceled", name, err)
		}
	}
	if _, _, err := p.Invoking(ctx, agent.InvokingContext{Options: []agent.Option{agent.WithSession(session)}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("invocation error = %v, want context.Canceled", err)
	}
	if readStore(t, store, "notes.md") != "original" || readStore(t, store, "notes.md_description.md") != "summary" {
		t.Fatal("cancelled operations changed stored memories")
	}
}

func TestProvider_CancellationDoesNotWaitForWriteLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p, store, _, tools := newMemoryFixture(t, Config{})
		if err := p.writeLock.Acquire(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		defer p.writeLock.Release(1)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		go func() {
			_, err := tools[WriteToolName].Call(ctx, `{"fileName":"notes.md","content":"text"}`)
			result <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want cancellation while the write lock remains held", err)
		}
		if names, err := store.ListChildren(t.Context(), ""); err != nil || len(names) != 0 {
			t.Fatalf("cancelled write changed files: %v, %v", names, err)
		}
	})
}

func TestProvider_EmptyIndexIsNotInjected(t *testing.T) {
	for _, index := range []string{"", " \t\n"} {
		store := &memoryStore{files: map[string]string{"memories.md": index}}
		p := newProvider(store, Config{})
		messages, _, err := p.Invoking(t.Context(), agent.InvokingContext{})
		if err != nil || len(messages) != 0 {
			t.Fatalf("blank index injected context: %v, %v", messages, err)
		}
	}
}

func TestProvider_RejectsInvalidToolArguments(t *testing.T) {
	_, store, _, tools := newMemoryFixture(t, Config{})
	for name, args := range map[string]string{
		WriteToolName:        `{"fileName":"notes.md"}`,
		ReadFileToolName:     `{"fileName":42}`,
		GrepToolName:         `{"regexPattern":"["}`,
		LsToolName:           `{"globPattern":42}`,
		ReplaceLinesToolName: `{"fileName":"notes.md","edits":[{"line_number":1}]}`,
	} {
		if _, err := tools[name].Call(t.Context(), args); err == nil {
			t.Fatalf("%s accepted invalid arguments %s", name, args)
		}
	}
	if names, err := store.ListChildren(t.Context(), ""); err != nil || len(names) != 0 {
		t.Fatalf("invalid arguments changed files: %v, %v", names, err)
	}
}

func TestProvider_AgentRunsUseMemoryToolsAndIndex(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			store := &memoryStore{files: make(map[string]string)}
			p := newProvider(store, Config{})
			assertResult := func(callID, want string) func(context.Context, []*message.Message, ...agent.Option) {
				return func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					for _, msg := range messages {
						for _, content := range msg.Contents {
							if result, ok := content.(*message.FunctionResultContent); ok && result.CallID == callID {
								if result.Error != nil || result.Result != want {
									t.Fatalf("tool result = %v, %v, want %q", result.Result, result.Error, want)
								}
								return
							}
						}
					}
					t.Fatalf("missing result for %s", callID)
				}
			}
			assertIndex := func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
				for _, msg := range messages {
					if msg.Source.ID == "FileMemoryProvider" && strings.Contains(msg.Contents.Text(), "- **notes.md**: summary") {
						return
					}
				}
				t.Fatal("next agent run did not receive the memory index")
			}
			runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().
				AddFunctionCall("write-1", WriteToolName, `{"fileName":"notes.md","content":"remembered content","description":"summary"}`).
				NewTurn(assertResult("write-1", "File 'notes.md' written with description.")).
				AddText("saved").
				NewTurn(assertIndex).
				AddFunctionCall("read-1", ReadFileToolName, `{"fileName":"notes.md"}`).
				NewTurn(assertResult("read-1", "remembered content")).
				AddText("remembered").
				Build()}
			a := agent.New(agent.ProviderConfig{
				Run:         runner.Run,
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
			}, agent.Config{
				ContextProviders: []agent.ContextProvider{p},
			})
			session := &agent.Session{}
			for _, want := range []string{"saved", "remembered"} {
				response, err := a.RunText(t.Context(), "use your memory", agent.WithSession(session), agent.Stream(stream)).Collect()
				if err != nil || response.String() != want {
					t.Fatalf("agent response = %v, %v, want %s", response, err, want)
				}
			}
		})
	}
}

func TestProvider_ConcurrentToolsWithTodo(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			p, store, _, _ := newMemoryFixture(t, Config{})
			todos := todo.New(nil)
			builder := agenttest.NewResponseBuilder()
			const count = 20
			for i := range count {
				builder.
					AddFunctionCall(fmt.Sprintf("todo-%d", i), "todos_add", fmt.Sprintf(`{"Arg0":[{"title":"Task %d"}]}`, i)).
					AddFunctionCall(fmt.Sprintf("memory-%d", i), WriteToolName, fmt.Sprintf(`{"fileName":"note%02d.md","content":"remembered"}`, i))
			}
			builder.NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
				var results int
				for _, msg := range messages {
					for _, content := range msg.Contents {
						if result, ok := content.(*message.FunctionResultContent); ok {
							results++
							if result.Error != nil {
								t.Errorf("tool %s failed: %v", result.CallID, result.Error)
							}
						}
					}
				}
				if results != 2*count {
					t.Errorf("received %d tool results, want %d", results, 2*count)
				}
			}).AddText("saved")
			runner := &agenttest.Runner{Responses: builder.Build()}
			a := agent.New(agent.ProviderConfig{
				Run: runner.Run,
				Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{
					AllowConcurrentInvocations: true,
				})},
			}, agent.Config{
				ContextProviders: []agent.ContextProvider{p, todos},
			})
			session := &agent.Session{}
			response, err := a.RunText(t.Context(), "save memories and add tasks", agent.WithSession(session), agent.Stream(stream)).Collect()
			if err != nil || response.String() != "saved" {
				t.Fatalf("agent response = %v, %v", response, err)
			}
			if items := todos.AllTodos(session); len(items) != count {
				t.Fatalf("saved %d todos, want %d", len(items), count)
			}
			for i := range count {
				if got := readStore(t, store, fmt.Sprintf("note%02d.md", i)); got != "remembered" {
					t.Fatalf("memory %d = %q", i, got)
				}
			}
			if index := readStore(t, store, memoryIndexName); strings.Count(index, "- **") != count {
				t.Fatalf("concurrent tool calls lost index entries: %q", index)
			}
		})
	}
}

type failingMemoryStore struct {
	filestore.Store
	operation string
	path      string
	failure   error
}

func (s *failingMemoryStore) fails(operation, path string) bool {
	return s.operation == operation && (s.path == "" || s.path == path)
}

func (s *failingMemoryStore) Read(ctx context.Context, name string) (string, error) {
	if s.fails("read", name) {
		return "", s.failure
	}
	return s.Store.Read(ctx, name)
}

func (s *failingMemoryStore) Write(ctx context.Context, name, content string) error {
	if s.fails("write", name) {
		return s.failure
	}
	return s.Store.Write(ctx, name, content)
}

func (s *failingMemoryStore) Delete(ctx context.Context, name string) error {
	if s.fails("delete", name) {
		return s.failure
	}
	return s.Store.Delete(ctx, name)
}

func (s *failingMemoryStore) ListChildren(ctx context.Context, directory string) ([]string, error) {
	if s.fails("list", directory) {
		return nil, s.failure
	}
	return s.Store.ListChildren(ctx, directory)
}

func (s *failingMemoryStore) CreateDirectory(ctx context.Context, directory string) error {
	if s.fails("create directory", directory) {
		return s.failure
	}
	return s.Store.CreateDirectory(ctx, directory)
}
