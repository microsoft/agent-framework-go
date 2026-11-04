// Copyright (c) Microsoft. All rights reserved.

// Package filestore provides SDK-internal root-confined text storage for agents.
//
// Store paths are relative to a store root. An empty directory names the root.
// FileSystemStore creates missing directories and confines operations to its
// root, rejecting absolute paths and paths that escape the root. Relative
// symbolic links are followed only when they remain within the root.
// Read and Delete report missing files with errors wrapping fs.ErrNotExist.
// ListChildren returns only direct regular files, not directories, symbolic links,
// or names that cannot be addressed through the store's path normalization.
//
// Contexts are checked before filesystem operations and during enumeration.
// A filesystem call already in progress cannot be interrupted.
// Individual store operations are safe for concurrent use; callers must
// synchronize sequences of operations that need to be performed together.
package filestore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/microsoft/agent-framework-go/internal/storepaths"
)

// Store stores text files under an implementation-defined root.
// Paths use forward slashes and must not escape that root. Implementations
// must be safe for concurrent use and honor context cancellation.
type Store interface {
	// Write creates or overwrites a file, creating its parent directories.
	Write(ctx context.Context, name, content string) error

	// Read returns a file's content. A missing file returns an error wrapping
	// [fs.ErrNotExist], which is distinct from an existing empty file.
	Read(ctx context.Context, name string) (string, error)

	// Delete removes a file, not a directory. A missing file returns an error
	// wrapping [fs.ErrNotExist].
	Delete(ctx context.Context, name string) error

	// ListChildren returns sorted names of regular files directly under directory.
	// Names are relative to that directory, without path separators. An empty
	// directory selects the root; a missing directory returns an empty list.
	// Names that cannot be addressed through the store's paths are omitted.
	ListChildren(ctx context.Context, directory string) ([]string, error)

	// CreateDirectory ensures a directory and its parents exist.
	// An empty directory selects the root.
	CreateDirectory(ctx context.Context, directory string) error
}

var _ Store = (*FileSystemStore)(nil)

// FileSystemStore stores text files beneath a filesystem directory.
// Use [NewFileSystemStore] to create a store and [FileSystemStore.Close] to release its root handle.
// Relative symbolic links within the root are allowed; listing omits symbolic links.
type FileSystemStore struct {
	mu   sync.RWMutex
	root *os.Root
}

// NewFileSystemStore opens a store rooted at rootDirectory, creating the
// directory if needed with permission 0700. New working directories use 0700
// and new files use 0600; existing permissions are unchanged.
func NewFileSystemStore(rootDirectory string) (*FileSystemStore, error) {
	if strings.TrimSpace(rootDirectory) == "" {
		return nil, fmt.Errorf("filestore: root directory must not be empty: %w", fs.ErrInvalid)
	}
	if err := os.MkdirAll(rootDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("filestore: create root directory: %w", err)
	}
	root, err := os.OpenRoot(rootDirectory)
	if err != nil {
		return nil, fmt.Errorf("filestore: open root directory: %w", err)
	}
	return &FileSystemStore{root: root}, nil
}

// Close releases the root directory handle. It is safe to call more than once.
// Subsequent operations return an error wrapping [fs.ErrClosed].
func (s *FileSystemStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	if err != nil {
		return fmt.Errorf("filestore: close store: %w", err)
	}
	return nil
}

// Write creates or overwrites a file and creates missing parent directories.
func (s *FileSystemStore) Write(ctx context.Context, name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalized, err := s.resolve(ctx, name, false)
	if err != nil {
		return err
	}
	if err := s.root.MkdirAll(path.Dir(normalized), 0o700); err != nil {
		return fmt.Errorf("filestore: create parent directory for %q: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.root.WriteFile(normalized, []byte(content), 0o600); err != nil {
		return fmt.Errorf("filestore: write %q: %w", name, err)
	}
	return nil
}

// Read returns a file's content, or an error wrapping [fs.ErrNotExist] if missing.
func (s *FileSystemStore) Read(ctx context.Context, name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	normalized, err := s.resolve(ctx, name, false)
	if err != nil {
		return "", err
	}
	data, err := s.root.ReadFile(normalized)
	if err != nil {
		return "", fmt.Errorf("filestore: read %q: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(data), nil
}

// Delete removes a file, or returns an error wrapping [fs.ErrNotExist] if missing.
func (s *FileSystemStore) Delete(ctx context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalized, err := s.resolve(ctx, name, false)
	if err != nil {
		return err
	}
	if err := s.root.Remove(normalized); err != nil {
		return fmt.Errorf("filestore: delete %q: %w", name, err)
	}
	return nil
}

// ListChildren returns sorted names of regular files directly under directory.
// Directories, symbolic links and unaddressable names are omitted.
// A missing directory is empty.
func (s *FileSystemStore) ListChildren(ctx context.Context, directory string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	normalized, err := s.resolve(ctx, directory, true)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(s.root.FS(), normalized)
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("filestore: list %q: %w", directory, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Type()&fs.ModeSymlink != 0 || entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("filestore: inspect %q: %w", entry.Name(), err)
		}
		if info.Mode().IsRegular() {
			name, err := storepaths.Normalize(entry.Name(), false)
			if err != nil || name != entry.Name() {
				continue
			}
			names = append(names, name)
		}
	}
	return names, nil
}

// CreateDirectory ensures a directory and its parents exist beneath the root.
func (s *FileSystemStore) CreateDirectory(ctx context.Context, directory string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalized, err := s.resolve(ctx, directory, true)
	if err != nil {
		return err
	}
	if err := s.root.MkdirAll(normalized, 0o700); err != nil {
		return fmt.Errorf("filestore: create directory %q: %w", directory, err)
	}
	return nil
}

func (s *FileSystemStore) resolve(ctx context.Context, name string, directory bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.root == nil {
		return "", fmt.Errorf("filestore: store is closed: %w", fs.ErrClosed)
	}
	normalized, err := storepaths.Normalize(name, directory)
	if err != nil {
		return "", fmt.Errorf("filestore: %w", err)
	}
	if normalized == "" {
		normalized = "."
	}
	info, err := s.root.Stat(normalized)
	if errors.Is(err, fs.ErrNotExist) {
		return normalized, nil
	}
	if err != nil {
		return "", fmt.Errorf("filestore: inspect path %q: %w", name, err)
	}
	if directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return "", fmt.Errorf("filestore: unexpected file type in path %q: %w", name, fs.ErrInvalid)
	}
	return normalized, nil
}
