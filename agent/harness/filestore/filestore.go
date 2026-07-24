// Copyright (c) Microsoft. All rights reserved.

// Package filestore provides file-store primitives for harness context providers.
package filestore

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
)

const (
	// EntryTypeFile identifies a regular file entry.
	EntryTypeFile = "file"
	// EntryTypeDirectory identifies a directory entry.
	EntryTypeDirectory = "directory"
)

// FileStore provides relative-path file storage for harness providers.
type FileStore interface {
	Write(context.Context, string, string) error
	Read(context.Context, string) (content string, found bool, err error)
	Delete(context.Context, string) (deleted bool, err error)
	ListChildren(context.Context, string) ([]Entry, error)
	FileExists(context.Context, string) (bool, error)
	Search(context.Context, string, string, string, bool) ([]SearchResult, error)
	CreateDirectory(context.Context, string) error
}

// Entry represents a direct child of a directory in a [FileStore].
type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// SearchMatch represents a single regex match line in a file.
type SearchMatch struct {
	LineNumber int    `json:"lineNumber"`
	Line       string `json:"line"`
}

// SearchResult represents a file matched by [FileStore.Search].
type SearchResult struct {
	FileName      string        `json:"fileName"`
	Snippet       string        `json:"snippet"`
	MatchingLines []SearchMatch `json:"matchingLines"`
}

// LineEdit represents a whole-line replacement operation.
type LineEdit struct {
	LineNumber int    `json:"line_number" jsonschema:"1-based line number to replace."`
	NewLine    string `json:"new_line" jsonschema:"Literal replacement text for the line, including any trailing newline you want to keep (the editor does not add one). Set to an empty string to delete the line entirely, including its line break."`
}

// InMemoryStore is an in-memory [FileStore] implementation.
type InMemoryStore struct {
	mu    sync.RWMutex
	files map[string]fileEntry
}

type fileEntry struct {
	path    string
	content string
}

// NewInMemoryStore creates a new in-memory [FileStore].
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{files: map[string]fileEntry{}}
}

// Write creates or overwrites a file.
func (s *InMemoryStore) Write(_ context.Context, path, content string) error {
	normalized, err := normalizeRelativePath(path, false)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[foldPath(normalized)] = fileEntry{path: normalized, content: content}
	return nil
}

// Read returns the content of a file when present.
func (s *InMemoryStore) Read(_ context.Context, path string) (string, bool, error) {
	normalized, err := normalizeRelativePath(path, false)
	if err != nil {
		return "", false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.files[foldPath(normalized)]
	if !ok {
		return "", false, nil
	}
	return entry.content, true, nil
}

// Delete removes a file when present.
func (s *InMemoryStore) Delete(_ context.Context, path string) (bool, error) {
	normalized, err := normalizeRelativePath(path, false)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := foldPath(normalized)
	if _, ok := s.files[key]; !ok {
		return false, nil
	}
	delete(s.files, key)
	return true, nil
}

// ListChildren returns the direct children of a directory.
func (s *InMemoryStore) ListChildren(ctx context.Context, directory string) ([]Entry, error) {
	prefix, err := normalizeRelativePath(directory, true)
	if err != nil {
		return nil, err
	}
	if prefix != "" {
		prefix += "/"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	directories := make([]string, 0)
	seenDirectories := map[string]struct{}{}
	files := make([]string, 0)

	for _, entry := range s.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(strings.ToLower(entry.path), strings.ToLower(prefix)) {
			continue
		}
		remainder := entry.path[len(prefix):]
		if remainder == "" {
			continue
		}
		if idx := strings.IndexByte(remainder, '/'); idx >= 0 {
			segment := remainder[:idx]
			key := foldPath(segment)
			if _, ok := seenDirectories[key]; ok {
				continue
			}
			seenDirectories[key] = struct{}{}
			directories = append(directories, segment)
			continue
		}
		files = append(files, remainder)
	}

	sortFolded(directories)
	sortFolded(files)

	entries := make([]Entry, 0, len(directories)+len(files))
	for _, name := range directories {
		entries = append(entries, Entry{Name: name, Type: EntryTypeDirectory})
	}
	for _, name := range files {
		entries = append(entries, Entry{Name: name, Type: EntryTypeFile})
	}
	return entries, nil
}

// FileExists reports whether a file exists.
func (s *InMemoryStore) FileExists(_ context.Context, path string) (bool, error) {
	normalized, err := normalizeRelativePath(path, false)
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.files[foldPath(normalized)]
	return ok, nil
}

// Search returns files whose contents match regexPattern.
func (s *InMemoryStore) Search(ctx context.Context, directory, regexPattern, globPattern string, recursive bool) ([]SearchResult, error) {
	prefix, err := normalizeRelativePath(directory, true)
	if err != nil {
		return nil, err
	}
	if prefix != "" {
		prefix += "/"
	}
	regex, err := regexp.Compile("(?i)" + regexPattern)
	if err != nil {
		return nil, err
	}
	glob, err := compileGlob(globPattern)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]SearchResult, 0)
	for _, entry := range s.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(strings.ToLower(entry.path), strings.ToLower(prefix)) {
			continue
		}
		relativeName := entry.path[len(prefix):]
		if relativeName == "" {
			continue
		}
		if !recursive && strings.Contains(relativeName, "/") {
			continue
		}
		if !matchesGlob(relativeName, glob) {
			continue
		}

		matchingLines, snippet := searchContent(regex, entry.content)
		if len(matchingLines) == 0 {
			continue
		}
		results = append(results, SearchResult{
			FileName:      relativeName,
			Snippet:       snippet,
			MatchingLines: matchingLines,
		})
	}

	slices.SortFunc(results, func(a, b SearchResult) int {
		return strings.Compare(strings.ToLower(a.FileName), strings.ToLower(b.FileName))
	})
	return results, nil
}

// CreateDirectory ensures a directory exists. In-memory directories are implicit.
func (s *InMemoryStore) CreateDirectory(_ context.Context, path string) error {
	_, err := normalizeRelativePath(path, true)
	return err
}

// ApplyReplace replaces oldString with newString in content.
func ApplyReplace(content, oldString, newString string, replaceAll bool) (string, int, error) {
	if oldString == "" {
		return "", 0, fmt.Errorf("old_string must not be empty")
	}
	count := strings.Count(content, oldString)
	if count == 0 {
		return "", 0, fmt.Errorf("old_string not found: %q", oldString)
	}
	if count > 1 && !replaceAll {
		return "", 0, fmt.Errorf("old_string occurs %d times; pass replace_all=true to replace all, or provide a more specific old_string", count)
	}
	if replaceAll {
		return strings.ReplaceAll(content, oldString, newString), count, nil
	}
	return strings.Replace(content, oldString, newString, 1), count, nil
}

// ApplyReplaceLines applies 1-based whole-line edits to content.
func ApplyReplaceLines(content string, edits []LineEdit) (string, error) {
	if len(edits) == 0 {
		return "", fmt.Errorf("at least one line edit must be provided")
	}
	lines := splitLinesKeepEnds(content)
	seen := map[int]struct{}{}
	for _, edit := range edits {
		if _, ok := seen[edit.LineNumber]; ok {
			return "", fmt.Errorf("duplicate line_number %d in edits", edit.LineNumber)
		}
		seen[edit.LineNumber] = struct{}{}
		if edit.LineNumber < 1 || edit.LineNumber > len(lines) {
			return "", fmt.Errorf("line_number %d is out of range (file has %d lines)", edit.LineNumber, len(lines))
		}
	}
	for _, edit := range edits {
		lines[edit.LineNumber-1] = edit.NewLine
	}
	return strings.Join(lines, ""), nil
}

func normalizeRelativePath(path string, isDirectory bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		if isDirectory {
			return "", nil
		}
		return "", fmt.Errorf("a file path must not be empty or whitespace-only")
	}

	normalized := strings.Trim(strings.ReplaceAll(path, "\\", "/"), "/")
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") || hasDriveRoot(normalized) {
		return "", fmt.Errorf("invalid path %q: paths must be relative and must not start with '/', '\\\\', or a drive root", path)
	}

	segments := strings.Split(normalized, "/")
	clean := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid path %q: paths must not contain '.' or '..' segments", path)
		}
		clean = append(clean, segment)
	}

	result := strings.Join(clean, "/")
	if result == "" && !isDirectory {
		return "", fmt.Errorf("a file path must not be empty")
	}
	return result, nil
}

func hasDriveRoot(path string) bool {
	return len(path) >= 2 && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) && path[1] == ':'
}

func compileGlob(pattern string) (*regexp.Regexp, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString(`(?:.*/)?`)
					i += 2
					continue
				}
				b.WriteString(".*")
				i++
				continue
			}
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func matchesGlob(name string, glob *regexp.Regexp) bool {
	return glob == nil || glob.MatchString(name)
}

func searchContent(regex *regexp.Regexp, content string) ([]SearchMatch, string) {
	lines := strings.Split(content, "\n")
	matches := make([]SearchMatch, 0)
	firstSnippet := ""
	offset := 0

	for i, line := range lines {
		match := regex.FindStringIndex(line)
		if match != nil {
			matches = append(matches, SearchMatch{LineNumber: i + 1, Line: strings.TrimSuffix(line, "\r")})
			if firstSnippet == "" {
				charIndex := offset + match[0]
				start := max(0, charIndex-50)
				end := min(len(content), offset+match[1]+50)
				firstSnippet = content[start:end]
			}
		}
		offset += len(line) + 1
	}
	return matches, firstSnippet
}

func splitLinesKeepEnds(content string) []string {
	lines := make([]string, 0)
	start := 0
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case '\n':
			lines = append(lines, content[start:i+1])
			start = i + 1
		case '\r':
			end := i + 1
			if end < len(content) && content[end] == '\n' {
				end++
			}
			lines = append(lines, content[start:end])
			i = end - 1
			start = end
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func sortFolded(values []string) {
	slices.SortFunc(values, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
}

func foldPath(path string) string {
	return strings.ToLower(path)
}
