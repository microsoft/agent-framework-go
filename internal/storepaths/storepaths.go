// Copyright (c) Microsoft. All rights reserved.

// Package storepaths normalizes relative paths shared by file stores and memory tools.
package storepaths

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// Normalize cleans a local relative path and returns forward-slash separators.
// Backslashes are accepted as separators on every platform. Rooted paths, paths
// invalid on the host platform, root escapes and NUL bytes return an error wrapping
// [fs.ErrInvalid]. Empty or whitespace-only paths select the root only when
// directory is true. A cleaned "." also selects the root only for directories.
// Non-empty path components must not be whitespace-only. Volume names and
// reserved names follow the host platform's filepath.IsLocal rules.
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
	for _, component := range strings.Split(normalized, "/") {
		if component == "" {
			continue
		}
		if strings.TrimSpace(component) == "" {
			return invalid()
		}
	}
	if !filepath.IsLocal(normalized) {
		return invalid()
	}
	normalized = filepath.ToSlash(filepath.Clean(normalized))
	if normalized == "." {
		if !directory {
			return invalid()
		}
		return "", nil
	}
	return normalized, nil
}
