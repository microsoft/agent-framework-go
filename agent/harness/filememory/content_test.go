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
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/microsoft/agent-framework-go/internal/filestore"
	"github.com/microsoft/agent-framework-go/internal/storepaths"
)

func TestMatchesGlob_WithMatcher(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"*.md", "notes.md", true},
		{"*.md", "notes.txt", false},
		{"research*", "research_results.md", true},
		{"research*", "notes.md", false},
		{"*.md", "NOTES.MD", true},
	} {
		t.Run(tc.pattern+"/"+tc.name, func(t *testing.T) {
			got, err := matchesGlob(tc.name, tc.pattern)
			if err != nil || got != tc.want {
				t.Fatalf("matchesGlob(%q, %q) = %v, %v, want %v", tc.name, tc.pattern, got, err, tc.want)
			}
		})
	}
}

func TestMatchesGlob_NullMatcher(t *testing.T) {
	matcher, err := compileGlob("")
	if err != nil || matcher != nil {
		t.Fatalf("compileGlob empty = %v, %v", matcher, err)
	}
	if got, err := matchesGlob("anything.txt", ""); err != nil || !got {
		t.Fatalf("unfiltered match = %v, %v", got, err)
	}
}

func TestApplyReplace_SingleOccurrence(t *testing.T) {
	got, count, err := replaceContent("Hello world", "world", "there", false)
	if err != nil || got != "Hello there" || count != 1 {
		t.Fatalf("replacement = %q, %d, %v", got, count, err)
	}
}

func TestApplyReplace_AllOccurrences(t *testing.T) {
	got, count, err := replaceContent("a a a", "a", "b", true)
	if err != nil || got != "b b b" || count != 3 {
		t.Fatalf("replacement = %q, %d, %v", got, count, err)
	}
}

func TestApplyReplace_EmptyOldString(t *testing.T) {
	if _, _, err := replaceContent("content", "", "x", false); err == nil {
		t.Fatal("expected an empty-search error")
	}
}

func TestApplyReplace_NotFound(t *testing.T) {
	if _, _, err := replaceContent("content", "missing", "x", false); err == nil {
		t.Fatal("expected a missing-search error")
	}
}

func TestApplyReplace_Ambiguous(t *testing.T) {
	if _, _, err := replaceContent("a a a", "a", "b", false); err == nil {
		t.Fatal("expected an ambiguous-search error")
	}
}

func assertLineReplacement(t *testing.T, content, want string, edits ...lineEdit) {
	t.Helper()
	got, err := replaceLines(content, edits)
	if err != nil || got != want {
		t.Fatalf("replaceLines = %q, %v, want %q", got, err, want)
	}
}

func TestApplyReplaceLines_SpecifiedLine(t *testing.T) {
	assertLineReplacement(t, "line1\nline2\nline3", "line1\nCHANGED\nline3", lineEdit{LineNumber: 2, NewLine: "CHANGED\n"})
}

func TestApplyReplaceLines_EmptyEdits(t *testing.T) {
	if _, err := replaceLines("line1", nil); err == nil {
		t.Fatal("expected an empty-edits error")
	}
}

func TestApplyReplaceLines_OutOfRange(t *testing.T) {
	if _, err := replaceLines("line1\nline2", []lineEdit{{LineNumber: 5, NewLine: "X"}}); err == nil {
		t.Fatal("expected an out-of-range error")
	}
}

func TestApplyReplaceLines_DuplicateLine(t *testing.T) {
	if _, err := replaceLines("line1\nline2", []lineEdit{{LineNumber: 1, NewLine: "A"}, {LineNumber: 1, NewLine: "B"}}); err == nil {
		t.Fatal("expected a duplicate-line error")
	}
}

func TestApplyReplaceLines_LiteralTrailingNewline(t *testing.T) {
	assertLineReplacement(t, "line1\nline2\n", "CHANGED\nline2\n", lineEdit{LineNumber: 1, NewLine: "CHANGED\n"})
}

func TestApplyReplaceLines_PreservesCRLF(t *testing.T) {
	assertLineReplacement(t, "line1\r\nline2\r\nline3", "line1\r\nCHANGED\r\nline3", lineEdit{LineNumber: 2, NewLine: "CHANGED\r\n"})
}

func TestApplyReplaceLines_DeletesMiddleLine(t *testing.T) {
	assertLineReplacement(t, "line1\nline2\nline3\n", "line1\nline3\n", lineEdit{LineNumber: 2})
}

func TestApplyReplaceLines_DeletesLastUnterminatedLine(t *testing.T) {
	assertLineReplacement(t, "line1\nline2", "line1\n", lineEdit{LineNumber: 2})
}

func TestApplyReplaceLines_DeleteAndReplace(t *testing.T) {
	assertLineReplacement(t, "a\nb\nc\n", "b\nC\n", lineEdit{LineNumber: 1}, lineEdit{LineNumber: 3, NewLine: "C\n"})
}

func TestApplyReplaceLines_EmbeddedNewline(t *testing.T) {
	assertLineReplacement(t, "a\nb\nc\n", "a\nb1\nb2\nc\n", lineEdit{LineNumber: 2, NewLine: "b1\nb2\n"})
}

func TestSplitLinesKeepEnds_KeepsTerminators(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    []string
	}{
		{"a\nb\nc", []string{"a\n", "b\n", "c"}},
		{"a\nb\n", []string{"a\n", "b\n"}},
		{"a\r\nb\r\n", []string{"a\r\n", "b\r\n"}},
		{"a\rb\rc", []string{"a\r", "b\r", "c"}},
		{"a\r\nb\nc\r", []string{"a\r\n", "b\n", "c\r"}},
		{"single", []string{"single"}},
		{"", []string{}},
	} {
		t.Run(fmt.Sprintf("%q", tc.content), func(t *testing.T) {
			if got := splitLines(tc.content); !slices.Equal(got, tc.want) {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitLinesKeepEnds_ConcatenationRoundTrip(t *testing.T) {
	const content = "alpha\r\nbeta\ngamma\rdelta"
	if got := strings.Join(splitLines(content), ""); got != content {
		t.Fatalf("round trip = %q, want %q", got, content)
	}
}

func TestLineContentLength_ExcludesOnlyTrailingTerminator(t *testing.T) {
	for _, tc := range [][2]string{{"match\r\n", "match"}, {"match\n", "match"}, {"match\r", "match"}, {"match", "match"}, {"", ""}, {"a\rb\n", "a\rb"}} {
		t.Run(fmt.Sprintf("%q", tc[0]), func(t *testing.T) {
			got := trimLineTerminator(tc[0])
			if len(got) != len(tc[1]) || got != tc[1] {
				t.Fatalf("line content = %q (length %d), want %q", got, len(got), tc[1])
			}
		})
	}
}

func TestLineContentLength_BoundsEndAnchoredMatch(t *testing.T) {
	re := regexp.MustCompile("(?i)match$")
	for _, line := range []string{"beta match\r\n", "beta match\n", "beta match\r", "beta match"} {
		t.Run(fmt.Sprintf("%q", line), func(t *testing.T) {
			match := re.FindStringIndex(trimLineTerminator(line))
			if len(match) != 2 || match[0] != 5 {
				t.Fatalf("end-anchored match = %v, want start 5", match)
			}
		})
	}
}

func TestFileStoreContract_ApplyReplaceLines_ExpectedLineMatching_AppliesTheEdit(t *testing.T) {
	assertLineReplacement(t, "one\ntwo\nthree\n", "one\nTWO\nthree\n", lineEdit{LineNumber: 2, NewLine: "TWO\n", ExpectedLine: new("two")})
}

func TestFileStoreContract_ApplyReplaceLines_ExpectedLineDiffering_Throws(t *testing.T) {
	_, err := replaceLines("one\ntwo\nthree\n", []lineEdit{{LineNumber: 3, NewLine: "X\n", ExpectedLine: new("two")}})
	if err == nil || !strings.Contains(err.Error(), "does not match expected_line") {
		t.Fatalf("stale edit error=%v", err)
	}
}

func TestFileStoreContract_ApplyReplaceLines_ExpectedLineIgnoresTheTerminator(t *testing.T) {
	assertLineReplacement(t, "alpha\r\nbeta\r\n", "alpha\r\nBETA\r\n", lineEdit{LineNumber: 2, NewLine: "BETA\r\n", ExpectedLine: new("beta\r\n")})
}

func TestFileStoreContract_ApplyReplaceLines_WithoutExpectedLine_IsUnchanged(t *testing.T) {
	assertLineReplacement(t, "one\ntwo\n", "ONE\ntwo\n", lineEdit{LineNumber: 1, NewLine: "ONE\n"})
}

func TestFileStoreContract_ScanContent_NumbersBySplitLines(t *testing.T) {
	const content = "alpha\r\nbeta match\r\ngamma\r\n"
	got, found, err := scanContent(t.Context(), "f.txt", content, regexp.MustCompile("(?i)match"))
	if err != nil || !found || len(got.MatchingLines) == 0 {
		t.Fatalf("scan=%+v, found=%v, err=%v", got, found, err)
	}
	match := got.MatchingLines[0]
	if match.LineNumber < 1 || match.LineNumber > len(splitLines(content)) || splitLines(content)[match.LineNumber-1] != match.Line || match.Line != "beta match\r\n" {
		t.Fatalf("scan line=%+v", match)
	}
}

func diskSearchStore(t *testing.T, files map[string]string) (*filestore.FileSystemStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := filestore.NewFileSystemStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	for name, content := range files {
		if err := store.Write(t.Context(), name, content); err != nil {
			t.Fatal(err)
		}
	}
	return store, root
}

func TestFileStore_SearchFilesAsync_FindsMatch(t *testing.T) {
	store, _ := diskSearchStore(t, map[string]string{"doc.md": "This has an error on line one.\nLine two is fine."})
	got, err := searchFiles(t.Context(), store, "", "error", "")
	if err != nil || len(got) != 1 || got[0].FileName != "doc.md" || len(got[0].MatchingLines) != 1 || got[0].MatchingLines[0].LineNumber != 1 || !strings.Contains(got[0].Snippet, "error") {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_ReportsLinesVerbatim(t *testing.T) {
	store, _ := diskSearchStore(t, map[string]string{"notes.md": "Line one\nLine two with match\nLine three\nLine four with match"})
	got, err := searchFiles(t.Context(), store, "", "match", "")
	if err != nil || len(got) != 1 || !slices.Equal(got[0].MatchingLines, []searchMatch{{LineNumber: 2, Line: "Line two with match\n"}, {LineNumber: 4, Line: "Line four with match"}}) {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_ReportsCrlfLinesVerbatim(t *testing.T) {
	store, _ := diskSearchStore(t, map[string]string{"notes.md": "alpha\r\nbeta match\r\ngamma\r\n"})
	got, err := searchFiles(t.Context(), store, "", "match", "")
	if err != nil || len(got) != 1 || !slices.Equal(got[0].MatchingLines, []searchMatch{{LineNumber: 2, Line: "beta match\r\n"}}) {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_TrailingNewline_DoesNotReportAnExtraLine(t *testing.T) {
	store, _ := diskSearchStore(t, map[string]string{"notes.md": "a\nb\n"})
	got, err := searchFiles(t.Context(), store, "", "^.*$", "")
	if err != nil || len(got) != 1 || len(got[0].MatchingLines) != 2 || got[0].MatchingLines[0].Line != "a\n" || got[0].MatchingLines[1].Line != "b\n" {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_LoneCarriageReturn_SplitsLikeTheLineEditor(t *testing.T) {
	store, _ := diskSearchStore(t, map[string]string{"notes.md": "alpha\rbeta match\rgamma"})
	got, err := searchFiles(t.Context(), store, "", "match", "")
	if err != nil || len(got) != 1 || !slices.Equal(got[0].MatchingLines, []searchMatch{{LineNumber: 2, Line: "beta match\r"}}) {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_EndAnchoredPatternMatchesRegardlessOfTerminator(t *testing.T) {
	for _, content := range []string{"alpha\r\nbeta match\r\ngamma\r\n", "alpha\rbeta match\rgamma", "alpha\nbeta match\ngamma\n"} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			store, _ := diskSearchStore(t, map[string]string{"notes.md": content})
			got, err := searchFiles(t.Context(), store, "", "match$", "")
			if err != nil || len(got) != 1 || len(got[0].MatchingLines) != 1 || got[0].MatchingLines[0].LineNumber != 2 {
				t.Fatalf("search=%+v, %v", got, err)
			}
		})
	}
}

func TestFileStore_SearchFilesAsync_SnippetIsAnchoredAtTheMatch(t *testing.T) {
	store, _ := diskSearchStore(t, map[string]string{"notes.md": strings.Repeat("x", 60) + "\nneedle\n"})
	got, err := searchFiles(t.Context(), store, "", "needle", "")
	if err != nil || len(got) != 1 || got[0].Snippet != strings.Repeat("x", 49)+"\nneedle\n" {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_GlobFilter_ExcludesNonMatching(t *testing.T) {
	store, _ := diskSearchStore(t, map[string]string{"notes.md": "important info", "data.txt": "important info"})
	got, err := searchFiles(t.Context(), store, "", "important", "*.md")
	if err != nil || len(got) != 1 || got[0].FileName != "notes.md" {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_NoMatch_ReturnsEmpty(t *testing.T) {
	store, _ := diskSearchStore(t, map[string]string{"doc.md": "nothing here"})
	if got, err := searchFiles(t.Context(), store, "", "missing-pattern", ""); err != nil || len(got) != 0 {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_NonExistentDirectory_ReturnsEmpty(t *testing.T) {
	store, _ := diskSearchStore(t, nil)
	if got, err := searchFiles(t.Context(), store, "no-dir", "anything", ""); err != nil || len(got) != 0 {
		t.Fatalf("search=%+v, %v", got, err)
	}
}

func TestFileStore_SearchFilesAsync_SymlinkedDirectory_Throws(t *testing.T) {
	store, root := diskSearchStore(t, nil)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "data.txt"), []byte("SENSITIVE_DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "search-link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := searchFiles(t.Context(), store, "search-link", "SENSITIVE", ""); err == nil {
		t.Fatal("searching through an escaping directory symlink succeeded")
	}
}

func TestFileStore_SearchFilesAsync_RootWithSymlinkedFile_DoesNotLeakContent(t *testing.T) {
	store, root := diskSearchStore(t, map[string]string{"normal.txt": "NORMAL_CONTENT"})
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("ROOT_LEVEL_SECRET_CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "env-link.txt")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if got, err := searchFiles(t.Context(), store, "", "SECRET_CONTENT", ""); err != nil || len(got) != 0 {
		t.Fatalf("search leaked outside content=%+v, %v", got, err)
	}
}

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
		{name: "notes.md", pattern: "./*.md", match: true},
		{name: "notes.md", pattern: "/*.md", match: true},
		{name: "notes.md", pattern: `\*.md`, match: true},
		{name: "notes.md", pattern: "/./**/./*.md", match: true},
		{name: "notes.md", pattern: ".//**//./*.md", match: true},
		{name: "README", pattern: "/*.*", match: true},
		{name: "README", pattern: `.\*.*`, match: true},
		{name: "notes.md", pattern: "nested/*.md"},
		{name: "notes.md", pattern: "nested/./*.md"},
		{name: "anything", match: true},
		{name: "anything", pattern: " \t", match: true},
		{name: "\u03c2.md", pattern: "\u03a3.md", match: true},
		{name: "\u03c3.md", pattern: "\u03c2.md", match: true},
		{name: "\u03a3.MD", pattern: "\u03c2.*", match: true},
		{name: "\u212a.md", pattern: "k.md", match: true},
		{name: "\u017f.md", pattern: "s.md", match: true},
		{name: "SS.md", pattern: "\u00df.md"},
		{name: "README", pattern: "*.*", match: true},
		{name: "notes.md", pattern: "*.*", match: true},
		{name: "notes.md", pattern: `**\*.md`, match: true},
		{name: "notes.md", pattern: `nested\*.md`},
		{name: "notes[1]+(a)?.md", pattern: "NOTES[1]+(A)?.MD", match: true},
		{name: "notes1.md", pattern: "notes[1].md"},
		{name: "notes1.md", pattern: "notes?.md"},
		{name: "notes?.md", pattern: "notes?.md", match: true},
		{name: "[", pattern: "[", match: true},
		{name: "notes.md", pattern: "["},
		{name: "notes.md\n", pattern: "notes.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.pattern, func(t *testing.T) {
			got, err := matchesGlob(tc.name, tc.pattern)
			if err != nil || got != tc.match {
				t.Fatalf("matchesGlob() = %v, %v, want %v", got, err, tc.match)
			}
		})
	}
	if _, err := matchesGlob("", "\xff"); err == nil {
		t.Fatal("expected invalid UTF-8 pattern error")
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

func TestSearchFiles_UnicodeCaseInsensitiveGlob(t *testing.T) {
	store := &memoryStore{files: map[string]string{"\u03c2.md": "matching content"}}
	results, err := searchFiles(t.Context(), store, "", "matching", "\u03a3.md")
	if err != nil || len(results) != 1 || results[0].FileName != "\u03c2.md" {
		t.Fatalf("Unicode glob search = %+v, %v", results, err)
	}
}

func TestSearchFiles_NormalizesPatternPrefixes(t *testing.T) {
	store := &memoryStore{files: map[string]string{"notes.md": "match", "README": "match"}}
	for _, pattern := range []string{"./*.md", "/*.md", `\*.md`, "/./**/*.md"} {
		results, err := searchFiles(t.Context(), store, "", "match", pattern)
		if err != nil || len(results) != 1 || results[0].FileName != "notes.md" {
			t.Fatalf("search pattern %q = %+v, %v", pattern, results, err)
		}
	}
	results, err := searchFiles(t.Context(), store, "", "match", "/*.*")
	if err != nil || len(results) != 2 {
		t.Fatalf("all-files pattern must include extensionless files: %+v, %v", results, err)
	}
	results, err = searchFiles(t.Context(), store, "", "match", "nested/*.md")
	if err != nil || len(results) != 0 {
		t.Fatalf("flat search must not match nested pattern: %+v, %v", results, err)
	}
}

func TestSearchFiles_SkipsUnaddressableFilesystemEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses filenames that Windows does not permit")
	}
	root := t.TempDir()
	store, err := filestore.NewFileSystemStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{`draft\notes.md`, "   "} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("match"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Write(t.Context(), "notes.md", "match"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), "C:notes.md", "match"); err != nil {
		t.Fatal(err)
	}
	results, err := searchFiles(t.Context(), store, "", "match", "*.md")
	if err != nil || len(results) != 2 || results[0].FileName != "C:notes.md" || results[1].FileName != "notes.md" {
		t.Fatalf("unsupported entries prevented a valid search: %+v, %v", results, err)
	}
}

func TestSearchFiles_LiteralPatternCharacters(t *testing.T) {
	store := &memoryStore{files: map[string]string{
		"notes[1].md": "matching content",
		"notes1.md":   "matching content",
	}}
	results, err := searchFiles(t.Context(), store, "", "matching", "notes[1].md")
	if err != nil || len(results) != 1 || results[0].FileName != "notes[1].md" {
		t.Fatalf("literal filename search = %+v, %v", results, err)
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
		{name: "invalid UTF-8 glob", glob: "\xff"},
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
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("after missing final file/partial=%v", partial), func(t *testing.T) {
			files := map[string]string{"z.md": "match"}
			if partial {
				files["a.md"] = "match"
			}
			base := &memoryStore{files: files}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			backend := &overriddenStore{Store: base, read: func(ctx context.Context, name string) (string, error) {
				if name == "z.md" {
					cancel()
					return "", fs.ErrNotExist
				}
				return base.Read(ctx, name)
			}}
			results, err := searchFiles(ctx, backend, "", "match", "")
			if !errors.Is(err, context.Canceled) || results != nil {
				t.Fatalf("search = %+v, %v, want cancellation without partial results", results, err)
			}
		})
	}
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

func (s *memoryStore) ListChildren(ctx context.Context, directory string) ([]string, error) {
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

func (s *overriddenStore) ListChildren(ctx context.Context, directory string) ([]string, error) {
	if s.list != nil {
		return s.list(ctx, directory)
	}
	return s.Store.ListChildren(ctx, directory)
}
