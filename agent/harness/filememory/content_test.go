// Copyright (c) Microsoft. All rights reserved.

package filememory

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/microsoft/agent-framework-go/agent/harness/filestore"
	"github.com/microsoft/agent-framework-go/internal/storepaths"
)

func TestReplaceContent(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		oldString  string
		newString  string
		replaceAll bool
		want       string
		count      int
		wantError  bool
	}{
		{name: "single", content: "hello world", oldString: "world", newString: "Go", want: "hello Go", count: 1},
		{name: "all", content: "one one one", oldString: "one", newString: "two", replaceAll: true, want: "two two two", count: 3},
		{name: "case sensitive", content: "aA", oldString: "a", newString: "b", want: "bA", count: 1},
		{name: "nonoverlapping", content: "aaaa", oldString: "aa", newString: "b", replaceAll: true, want: "bb", count: 2},
		{name: "delete", content: "hello world", oldString: " world", want: "hello", count: 1},
		{name: "whitespace", content: "one two", oldString: " ", newString: "-", want: "one-two", count: 1},
		{name: "empty old string", content: "text", wantError: true},
		{name: "not found", content: "text", oldString: "missing", wantError: true},
		{name: "ambiguous", content: "one one", oldString: "one", newString: "two", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, count, err := replaceContent(tc.content, tc.oldString, tc.newString, tc.replaceAll)
			if tc.wantError {
				if err == nil || got != "" || count != 0 {
					t.Fatalf("replaceContent() = %q, %d, %v, want error without replacements", got, count, err)
				}
				return
			}
			if err != nil || got != tc.want || count != tc.count {
				t.Fatalf("replaceContent() = %q, %d, %v, want %q, %d", got, count, err, tc.want, tc.count)
			}
		})
	}
}

func TestSplitLines(t *testing.T) {
	cases := []struct {
		name    string
		content string
		lines   []string
	}{
		{name: "empty"},
		{name: "unterminated", content: "one", lines: []string{"one"}},
		{name: "LF", content: "one\ntwo\n", lines: []string{"one\n", "two\n"}},
		{name: "CRLF", content: "one\r\ntwo\r\n", lines: []string{"one\r\n", "two\r\n"}},
		{name: "CR", content: "one\rtwo\r", lines: []string{"one\r", "two\r"}},
		{name: "mixed", content: "one\r\ntwo\rthree\nfour", lines: []string{"one\r\n", "two\r", "three\n", "four"}},
		{name: "blank lines", content: "\n\r\n\r", lines: []string{"\n", "\r\n", "\r"}},
		{name: "unicode", content: "\u03b1\r\n\u03b2", lines: []string{"\u03b1\r\n", "\u03b2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitLines(tc.content)
			if !slices.Equal(got, tc.lines) {
				t.Fatalf("splitLines() = %q, want %q", got, tc.lines)
			}
			if strings.Join(got, "") != tc.content {
				t.Fatal("joining lines changed the original content")
			}
		})
	}
}

func TestReplaceLines(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		edits     []lineEdit
		want      string
		wantError bool
	}{
		{name: "replace", content: "one\ntwo\nthree", edits: []lineEdit{{LineNumber: 2, NewLine: "changed\n"}}, want: "one\nchanged\nthree"},
		{name: "literal replacement", content: "one\ntwo\nthree", edits: []lineEdit{{LineNumber: 2, NewLine: "changed"}}, want: "one\nchangedthree"},
		{name: "CRLF preserved", content: "one\r\ntwo\r\n", edits: []lineEdit{{LineNumber: 2, NewLine: "changed\r\n"}}, want: "one\r\nchanged\r\n"},
		{name: "CR preserved", content: "one\rtwo\r", edits: []lineEdit{{LineNumber: 2, NewLine: "changed\r"}}, want: "one\rchanged\r"},
		{name: "delete middle", content: "one\ntwo\nthree", edits: []lineEdit{{LineNumber: 2}}, want: "one\nthree"},
		{name: "delete last", content: "one\ntwo", edits: []lineEdit{{LineNumber: 2}}, want: "one\n"},
		{name: "delete only line", content: "one", edits: []lineEdit{{LineNumber: 1}}},
		{name: "original coordinates", content: "one\ntwo\nthree", edits: []lineEdit{{LineNumber: 3, NewLine: "last"}, {LineNumber: 1}}, want: "two\nlast"},
		{name: "expand line", content: "one\ntwo", edits: []lineEdit{{LineNumber: 1, NewLine: "a\nb\n"}}, want: "a\nb\ntwo"},
		{name: "expected line", content: "one\r\ntwo", edits: []lineEdit{{LineNumber: 1, NewLine: "changed\n", ExpectedLine: new("one\n")}}, want: "changed\ntwo"},
		{name: "expected empty line", content: "\n", edits: []lineEdit{{LineNumber: 1, NewLine: "changed", ExpectedLine: new("")}}, want: "changed"},
		{name: "no edits", content: "one", wantError: true},
		{name: "empty content", edits: []lineEdit{{LineNumber: 1}}, wantError: true},
		{name: "zero line", content: "one", edits: []lineEdit{{LineNumber: 0}}, wantError: true},
		{name: "negative line", content: "one", edits: []lineEdit{{LineNumber: -1}}, wantError: true},
		{name: "out of range", content: "one\n", edits: []lineEdit{{LineNumber: 2}}, wantError: true},
		{name: "duplicate", content: "one", edits: []lineEdit{{LineNumber: 1}, {LineNumber: 1}}, wantError: true},
		{name: "stale expected line", content: "one", edits: []lineEdit{{LineNumber: 1, ExpectedLine: new("two")}}, wantError: true},
		{name: "empty expectation is enforced", content: "one", edits: []lineEdit{{LineNumber: 1, ExpectedLine: new("")}}, wantError: true},
		{name: "validate all edits", content: "one\ntwo", edits: []lineEdit{{LineNumber: 1, NewLine: "changed\n"}, {LineNumber: 3}}, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			originalEdits := slices.Clone(tc.edits)
			got, err := replaceLines(tc.content, tc.edits)
			if tc.wantError {
				if err == nil || got != "" {
					t.Fatalf("replaceLines() = %q, %v, want error without modified content", got, err)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("replaceLines() = %q, %v, want %q", got, err, tc.want)
			}
			if !slices.Equal(tc.edits, originalEdits) {
				t.Fatal("replaceLines changed the input edits")
			}
		})
	}
}

func TestLineEditJSON(t *testing.T) {
	var edits []lineEdit
	if err := json.Unmarshal([]byte(`[
		{"line_number":1,"new_line":"changed","expected_line":""},
		{"line_number":2,"new_line":""}
	]`), &edits); err != nil {
		t.Fatal(err)
	}
	if len(edits) != 2 || edits[0].ExpectedLine == nil || *edits[0].ExpectedLine != "" || edits[1].ExpectedLine != nil {
		t.Fatalf("explicitly empty and omitted expected_line must remain distinct: %+v", edits)
	}
}

func TestMatchesGlob(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		match   bool
	}{
		{name: "notes.md", pattern: "*.md", match: true},
		{name: "NOTES.MD", pattern: "*.md", match: true},
		{name: "notes.txt", pattern: "*.md"},
		{name: "research_results.md", pattern: "research*", match: true},
		{name: "notes.md", pattern: "research*"},
		{name: "notes.md", pattern: "**/*.md", match: true},
		{name: "notes.md", pattern: "**/**/notes.md", match: true},
		{name: "notes.md", pattern: "nested/*.md"},
		{name: "anything", match: true},
		{name: "anything", pattern: " \t", match: true},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.pattern, func(t *testing.T) {
			got, err := matchesGlob(tc.name, tc.pattern)
			if err != nil || got != tc.match {
				t.Fatalf("matchesGlob() = %v, %v, want %v", got, err, tc.match)
			}
		})
	}
	if _, err := matchesGlob("", "["); err == nil {
		t.Fatal("expected invalid glob error")
	}
}

func TestSearchFiles(t *testing.T) {
	factories := []struct {
		name string
		new  func(*testing.T) filestore.Store
	}{
		{name: "memory", new: func(*testing.T) filestore.Store { return &memoryStore{files: make(map[string]string)} }},
		{name: "filesystem", new: func(t *testing.T) filestore.Store {
			store, err := filestore.NewFileSystemStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			return store
		}},
	}
	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) {
			store := factory.new(t)
			files := map[string]string{
				"notes.md":       "first\r\nMATCH\rmatch\nmatch\r\nlast",
				"other.txt":      "match",
				"unmatched.md":   "nothing",
				"nested/data.md": "match",
			}
			for name, content := range files {
				if err := store.Write(t.Context(), name, content); err != nil {
					t.Fatal(err)
				}
			}
			results, err := searchFiles(t.Context(), store, "", "match$", "*.MD")
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].FileName != "notes.md" || results[0].Snippet != files["notes.md"] {
				t.Fatalf("unexpected search results: %+v", results)
			}
			want := []searchMatch{
				{LineNumber: 2, Line: "MATCH\r"},
				{LineNumber: 3, Line: "match\n"},
				{LineNumber: 4, Line: "match\r\n"},
			}
			if !slices.Equal(results[0].MatchingLines, want) {
				t.Fatalf("matching lines = %+v, want %+v", results[0].MatchingLines, want)
			}
			var edits []lineEdit
			for _, match := range results[0].MatchingLines {
				edits = append(edits, lineEdit{LineNumber: match.LineNumber, NewLine: match.Line, ExpectedLine: new(match.Line)})
			}
			roundTrip, err := replaceLines(files["notes.md"], edits)
			if err != nil || roundTrip != files["notes.md"] {
				t.Fatalf("search lines cannot be reused as edits: %q, %v", roundTrip, err)
			}
			results, err = searchFiles(t.Context(), store, "nested", "match", "**/*.md")
			if err != nil || len(results) != 1 || results[0].FileName != "data.md" {
				t.Fatalf("subdirectory search = %+v, %v", results, err)
			}
			results, err = searchFiles(t.Context(), store, "", "absent", "")
			if err != nil || len(results) != 0 {
				t.Fatalf("no-match search = %+v, %v", results, err)
			}
		})
	}
}

func TestSearchFiles_Snippet(t *testing.T) {
	for _, character := range []string{"a", "\u03b1", "\U0001f642"} {
		t.Run(character, func(t *testing.T) {
			content := strings.Repeat(character, 60) + "MATCH" + strings.Repeat(character, 60)
			result, found, err := scanContent(t.Context(), "notes.md", content, regexp.MustCompile("MATCH"))
			want := strings.Repeat(character, 50) + "MATCH" + strings.Repeat(character, 50)
			if err != nil || !found || result.Snippet != want || !utf8.ValidString(result.Snippet) {
				t.Fatalf("snippet = %q, found = %v, error = %v, want %q", result.Snippet, found, err, want)
			}
		})
	}
}

func TestSearchFiles_RejectsInvalidInput(t *testing.T) {
	store := &memoryStore{files: make(map[string]string)}
	for _, tc := range []struct {
		name      string
		directory string
		regex     string
		glob      string
	}{
		{name: "directory traversal", directory: "../outside"},
		{name: "absolute directory", directory: "/outside"},
		{name: "bad regex", regex: "["},
		{name: "unsupported regex", regex: `(?<=prefix)match`},
		{name: "bad glob", glob: "["},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := searchFiles(t.Context(), store, tc.directory, tc.regex, tc.glob); err == nil {
				t.Fatal("expected invalid input error, even for an empty store")
			}
		})
	}
	for _, name := range []string{"../outside.md", "/outside.md", "nested/notes.md", `nested\notes.md`, ""} {
		t.Run("listed "+name, func(t *testing.T) {
			backend := &overriddenStore{Store: store, list: func(context.Context, string) ([]string, error) {
				return []string{name}, nil
			}}
			if _, err := searchFiles(t.Context(), backend, "", "match", ""); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("error = %v, want fs.ErrInvalid", err)
			}
		})
	}
}

func TestSearchFiles_PropagatesErrors(t *testing.T) {
	failure := errors.New("storage failed")
	store := &memoryStore{files: map[string]string{"notes.md": "match"}}
	cases := []struct {
		name    string
		backend filestore.Store
	}{
		{name: "list", backend: &overriddenStore{Store: store, list: func(context.Context, string) ([]string, error) {
			return nil, failure
		}}},
		{name: "read", backend: &overriddenStore{Store: store, read: func(context.Context, string) (string, error) {
			return "", failure
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := searchFiles(t.Context(), tc.backend, "", "match", ""); !errors.Is(err, failure) {
				t.Fatalf("error = %v, want original storage error", err)
			}
		})
	}
}

func TestSearchFiles_DeletedAndFilteredFiles(t *testing.T) {
	store := &memoryStore{files: map[string]string{"deleted.md": "match", "keep.md": "MATCH", "skip.txt": "match"}}
	backend := &overriddenStore{Store: store, read: func(ctx context.Context, name string) (string, error) {
		switch name {
		case "deleted.md":
			return "", fs.ErrNotExist
		case "skip.txt":
			t.Fatal("glob-excluded file was read")
		}
		return store.Read(ctx, name)
	}}
	results, err := searchFiles(t.Context(), backend, "", "match", "*.md")
	if err != nil || len(results) != 1 || results[0].FileName != "keep.md" {
		t.Fatalf("search = %+v, %v", results, err)
	}
}

func TestSearchFiles_Cancellation(t *testing.T) {
	store := &memoryStore{files: map[string]string{"notes.md": "match"}}
	t.Run("before search", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := searchFiles(ctx, store, "", "match", ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
	t.Run("after empty listing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		backend := &overriddenStore{Store: store, list: func(context.Context, string) ([]string, error) {
			cancel()
			return nil, nil
		}}
		if _, err := searchFiles(ctx, backend, "", "match", ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
	t.Run("after empty read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		backend := &overriddenStore{Store: store, read: func(context.Context, string) (string, error) {
			cancel()
			return "", nil
		}}
		if _, err := searchFiles(ctx, backend, "", "match", ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
	t.Run("storage error takes precedence", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		failure := errors.New("storage failed")
		backend := &overriddenStore{Store: store, read: func(context.Context, string) (string, error) {
			cancel()
			return "", failure
		}}
		if _, err := searchFiles(ctx, backend, "", "match", ""); !errors.Is(err, failure) {
			t.Fatalf("error = %v, want original storage error", err)
		}
	})
}

func TestSearchResultJSON(t *testing.T) {
	result := searchResult{
		FileName: "notes.md", Snippet: "match\r\n",
		MatchingLines: []searchMatch{{LineNumber: 1, Line: "match\r\n"}},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"fileName":"notes.md","snippet":"match\r\n","matchingLines":[{"lineNumber":1,"line":"match\r\n"}]}`
	if string(data) != want {
		t.Fatalf("search result JSON = %s, want %s", data, want)
	}
}

type memoryStore struct {
	mu    sync.RWMutex
	files map[string]string
}

func (s *memoryStore) Write(ctx context.Context, name, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, err := storepaths.Normalize(name, false)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = content
	return nil
}

func (s *memoryStore) Read(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	content, found := s.files[name]
	if !found {
		return "", fs.ErrNotExist
	}
	return content, nil
}

func (s *memoryStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.files[name]; !found {
		return fs.ErrNotExist
	}
	delete(s.files, name)
	return nil
}

func (s *memoryStore) List(ctx context.Context, directory string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := directory
	if prefix != "" {
		prefix += "/"
	}
	names := make([]string, 0)
	for name := range s.files {
		if relative, found := strings.CutPrefix(name, prefix); found && !strings.ContainsRune(relative, '/') {
			names = append(names, relative)
		}
	}
	slices.Sort(names)
	return names, nil
}

func (s *memoryStore) CreateDirectory(ctx context.Context, directory string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := storepaths.Normalize(directory, true)
	return err
}

type overriddenStore struct {
	filestore.Store
	read func(context.Context, string) (string, error)
	list func(context.Context, string) ([]string, error)
}

func (s *overriddenStore) Read(ctx context.Context, name string) (string, error) {
	if s.read != nil {
		return s.read(ctx, name)
	}
	return s.Store.Read(ctx, name)
}

func (s *overriddenStore) List(ctx context.Context, directory string) ([]string, error) {
	if s.list != nil {
		return s.list(ctx, directory)
	}
	return s.Store.List(ctx, directory)
}
