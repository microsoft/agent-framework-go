// Copyright (c) Microsoft. All rights reserved.

// Package filememory provides search and editing support for file-based agent memory.
//
// Storage operations are supplied by filestore.Store. Searches inspect direct
// files using Go regular expressions and case-insensitive filename globs.
// Search matches and line edits share 1-based line numbering and preserve each
// line's LF, CRLF, or CR terminator.
package filememory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/microsoft/agent-framework-go/agent/harness/filestore"
	"github.com/microsoft/agent-framework-go/internal/storepaths"
)

type lineEdit struct {
	LineNumber   int     `json:"line_number"`
	NewLine      string  `json:"new_line"`
	ExpectedLine *string `json:"expected_line,omitempty"`
}

type searchMatch struct {
	LineNumber int    `json:"lineNumber"`
	Line       string `json:"line"`
}

type searchResult struct {
	FileName      string        `json:"fileName"`
	Snippet       string        `json:"snippet"`
	MatchingLines []searchMatch `json:"matchingLines"`
}

func replaceContent(content, oldString, newString string, replaceAll bool) (string, int, error) {
	if oldString == "" {
		return "", 0, fmt.Errorf("filememory: oldString must not be empty")
	}
	count := strings.Count(content, oldString)
	if count == 0 {
		return "", 0, fmt.Errorf("filememory: oldString not found: %q", oldString)
	}
	if count > 1 && !replaceAll {
		return "", 0, fmt.Errorf("filememory: oldString occurs %d times; set replaceAll to true or provide more specific text", count)
	}
	return strings.ReplaceAll(content, oldString, newString), count, nil
}

func replaceLines(content string, edits []lineEdit) (string, error) {
	if len(edits) == 0 {
		return "", fmt.Errorf("filememory: at least one line edit must be provided")
	}
	lines := splitLines(content)
	seen := make(map[int]struct{}, len(edits))
	for _, edit := range edits {
		if _, exists := seen[edit.LineNumber]; exists {
			return "", fmt.Errorf("filememory: duplicate line_number %d", edit.LineNumber)
		}
		seen[edit.LineNumber] = struct{}{}
		if edit.LineNumber < 1 || edit.LineNumber > len(lines) {
			return "", fmt.Errorf("filememory: line_number %d is out of range; file has %d lines", edit.LineNumber, len(lines))
		}
		if edit.ExpectedLine != nil && trimLineTerminator(lines[edit.LineNumber-1]) != trimLineTerminator(*edit.ExpectedLine) {
			return "", fmt.Errorf("filememory: line_number %d does not match expected_line; read the file again to get current line numbers", edit.LineNumber)
		}
	}
	for _, edit := range edits {
		lines[edit.LineNumber-1] = edit.NewLine
	}
	return strings.Join(lines, ""), nil
}

func splitLines(content string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case '\n':
			lines = append(lines, content[start:i+1])
			start = i + 1
		case '\r':
			if i+1 < len(content) && content[i+1] == '\n' {
				i++
			}
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func trimLineTerminator(line string) string {
	if strings.HasSuffix(line, "\r\n") {
		return line[:len(line)-2]
	}
	if strings.HasSuffix(line, "\n") || strings.HasSuffix(line, "\r") {
		return line[:len(line)-1]
	}
	return line
}

func matchesGlob(name, pattern string) (bool, error) {
	if strings.TrimSpace(pattern) == "" {
		return true, nil
	}
	// Searches are flat; a leading **/ can also match a direct child.
	for strings.HasPrefix(pattern, "**/") {
		pattern = strings.TrimPrefix(pattern, "**/")
	}
	matched, err := path.Match(strings.ToLower(pattern), strings.ToLower(name))
	if err != nil {
		return false, fmt.Errorf("filememory: invalid glob pattern %q: %w", pattern, err)
	}
	return matched, nil
}

func searchFiles(ctx context.Context, store filestore.Store, directory, regexPattern, globPattern string) ([]searchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := storepaths.Normalize(directory, true)
	if err != nil {
		return nil, fmt.Errorf("filememory: %w", err)
	}
	expression, err := regexp.Compile("(?i)" + regexPattern)
	if err != nil {
		return nil, fmt.Errorf("filememory: invalid regular expression %q: %w", regexPattern, err)
	}
	if _, err := matchesGlob("", globPattern); err != nil {
		return nil, err
	}
	names, err := store.List(ctx, directory)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make([]searchResult, 0)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		normalized, err := storepaths.Normalize(name, false)
		if err != nil {
			return nil, fmt.Errorf("filememory: %w", err)
		}
		if normalized != name || strings.ContainsRune(normalized, '/') {
			return nil, fmt.Errorf("filememory: invalid listed file name %q: %w", name, fs.ErrInvalid)
		}
		matched, err := matchesGlob(name, globPattern)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		fileName := name
		if directory != "" {
			fileName = directory + "/" + name
		}
		content, err := store.Read(ctx, fileName)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result, found, err := scanContent(ctx, name, content, expression)
		if err != nil {
			return nil, err
		}
		if found {
			results = append(results, result)
		}
	}
	return results, nil
}

func scanContent(ctx context.Context, name, content string, expression *regexp.Regexp) (searchResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return searchResult{}, false, err
	}
	result := searchResult{FileName: name}
	offset := 0
	for i, line := range splitLines(content) {
		if err := ctx.Err(); err != nil {
			return searchResult{}, false, err
		}
		match := expression.FindStringIndex(trimLineTerminator(line))
		if match != nil {
			if len(result.MatchingLines) == 0 {
				result.Snippet = matchSnippet(content, offset+match[0], offset+match[1])
			}
			result.MatchingLines = append(result.MatchingLines, searchMatch{LineNumber: i + 1, Line: line})
		}
		offset += len(line)
	}
	return result, len(result.MatchingLines) != 0, nil
}

func matchSnippet(content string, start, end int) string {
	for i := 0; i < 50 && start > 0; i++ {
		_, size := utf8.DecodeLastRuneInString(content[:start])
		start -= size
	}
	for i := 0; i < 50 && end < len(content); i++ {
		_, size := utf8.DecodeRuneInString(content[end:])
		end += size
	}
	return content[start:end]
}
