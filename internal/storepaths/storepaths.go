// Copyright (c) Microsoft. All rights reserved.

// Package storepaths normalizes relative paths shared by file stores and memory tools.
package storepaths

import (
	"fmt"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Normalize replaces backslashes with forward slashes, collapses repeated
// separators and removes trailing separators. Rooted paths, drive prefixes,
// traversal segments and NUL bytes are rejected with an error wrapping
// [fs.ErrInvalid]. Empty or whitespace-only paths select the root only when
// directory is true.
func Normalize(name string, directory bool) (string, error) {
	invalid := func() (string, error) {
		return "", fmt.Errorf("invalid relative path %q: %w", name, fs.ErrInvalid)
	}
	if strings.TrimSpace(name) == "" {
		if directory {
			return "", nil
		}
		return invalid()
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") || strings.ContainsRune(name, '\x00') {
		return invalid()
	}
	normalized := strings.ReplaceAll(name, "\\", "/")
	first, size := utf8.DecodeRuneInString(normalized)
	if unicode.IsLetter(first) && len(normalized) > size && normalized[size] == ':' {
		return invalid()
	}
	segments := strings.Split(normalized, "/")
	clean := segments[:0]
	for _, segment := range segments {
		switch segment {
		case ".", "..":
			return invalid()
		case "":
			continue
		default:
			clean = append(clean, segment)
		}
	}
	normalized = strings.Join(clean, "/")
	if normalized == "" && !directory {
		return invalid()
	}
	return normalized, nil
}
