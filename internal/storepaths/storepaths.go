// Copyright (c) Microsoft. All rights reserved.

// Package storepaths normalizes relative paths shared by file stores and memory tools.
package storepaths

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Normalize cleans a local relative path and returns forward-slash separators.
// Backslashes are accepted as separators on every platform. Rooted paths, drive
// prefixes, paths escaping the root and NUL bytes return an error wrapping
// [fs.ErrInvalid]. Empty or whitespace-only paths select the root only when
// directory is true. A cleaned "." also selects the root only for directories.
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
	if strings.ContainsRune(name, '\x00') {
		return invalid()
	}
	normalized := strings.ReplaceAll(name, "\\", "/")
	first, size := utf8.DecodeRuneInString(normalized)
	if unicode.IsLetter(first) && len(normalized) > size && normalized[size] == ':' {
		return invalid()
	}
	local := filepath.FromSlash(normalized)
	if !filepath.IsLocal(local) {
		return invalid()
	}
	normalized = filepath.ToSlash(filepath.Clean(local))
	if normalized == "." {
		if !directory {
			return invalid()
		}
		return "", nil
	}
	return normalized, nil
}
