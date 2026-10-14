// Copyright (c) Microsoft. All rights reserved.

package filestore_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/harness/filestore"
)

func newStore(t *testing.T) (*filestore.FileSystemStore, string) {
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
	return store, root
}

func storeOperations(store filestore.Store, name string) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"write": func(ctx context.Context) error { return store.Write(ctx, name, "changed") },
		"read": func(ctx context.Context) error {
			_, err := store.Read(ctx, name)
			return err
		},
		"delete": func(ctx context.Context) error { return store.Delete(ctx, name) },
		"list": func(ctx context.Context) error {
			_, err := store.List(ctx, name)
			return err
		},
		"create directory": func(ctx context.Context) error { return store.CreateDirectory(ctx, name) },
	}
}

func TestNewFileSystemStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new", "store")
	store, err := filestore.NewFileSystemStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("store root is not a directory")
	}
	if err := store.Write(t.Context(), "notes.md", "notes"); err != nil {
		t.Fatal(err)
	}
}

func TestNewFileSystemStore_RejectsInvalidRoot(t *testing.T) {
	for _, name := range []string{"", " \t\n"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			_, err := filestore.NewFileSystemStore(name)
			if !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("error = %v, want fs.ErrInvalid", err)
			}
		})
	}
	root := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(root, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := filestore.NewFileSystemStore(root); err == nil {
		t.Fatal("expected error when root is a file")
	}
}

func TestFileSystemStore_WriteReadDelete(t *testing.T) {
	for _, content := range []string{"", "notes", "one\r\ntwo\rthree\n", "\u03b1\u03b2\u03b3 \u00e9"} {
		t.Run(fmt.Sprintf("%q", content), func(t *testing.T) {
			store, _ := newStore(t)
			ctx := t.Context()
			if err := store.Write(ctx, "work/notes.md", content); err != nil {
				t.Fatal(err)
			}
			got, err := store.Read(ctx, "work/notes.md")
			if err != nil {
				t.Fatal(err)
			}
			if got != content {
				t.Fatalf("Read() = %q, want %q", got, content)
			}
			if err := store.Write(ctx, "work/notes.md", "x"); err != nil {
				t.Fatal(err)
			}
			got, err = store.Read(ctx, "work/notes.md")
			if err != nil || got != "x" {
				t.Fatalf("Read() after overwrite = %q, %v, want x", got, err)
			}
			if err := store.Delete(ctx, "work/notes.md"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Read(ctx, "work/notes.md"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Read() after delete = %v, want fs.ErrNotExist", err)
			}
			if err := store.Delete(ctx, "work/notes.md"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Delete() missing file = %v, want fs.ErrNotExist", err)
			}
		})
	}
}

func TestFileSystemStore_List(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()
	for _, name := range []string{"z.md", "a.md", "nested/hidden.md"} {
		if err := store.Write(ctx, name, "text"); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{"", " \t"} {
		names, err := store.List(ctx, directory)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(names, []string{"a.md", "z.md"}) {
			t.Fatalf("List(%q) = %v", directory, names)
		}
	}
	if err := store.CreateDirectory(ctx, "empty/deep"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateDirectory(ctx, ""); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"missing", "empty", "empty/deep"} {
		names, err := store.List(ctx, directory)
		if err != nil || len(names) != 0 {
			t.Fatalf("List(%q) = %v, %v, want empty list", directory, names, err)
		}
	}
	names, err := store.List(ctx, "nested")
	if err != nil || !slices.Equal(names, []string{"hidden.md"}) {
		t.Fatalf("List(nested) = %v, %v", names, err)
	}
}

func TestFileSystemStore_NormalizesPaths(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()
	if err := store.Write(ctx, `work\\nested//notes.md/`, "text"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read(ctx, "work/nested/notes.md")
	if err != nil || got != "text" {
		t.Fatalf("Read() = %q, %v", got, err)
	}
	names, err := store.List(ctx, `work\nested/`)
	if err != nil || !slices.Equal(names, []string{"notes.md"}) {
		t.Fatalf("List() = %v, %v", names, err)
	}
	if err := store.CreateDirectory(ctx, `other\\nested//`); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, `work\\nested\notes.md/`); err != nil {
		t.Fatal(err)
	}
}

func TestFileSystemStore_RejectsInvalidPaths(t *testing.T) {
	store, _ := newStore(t)
	for _, name := range []string{
		"..", "../escape.md", "work/../escape.md", "./notes.md", "work/./notes.md",
		"/absolute.md", `\absolute.md`, `\\server\file.md`, `C:\notes.md`,
		"C:/notes.md", "D:notes.md", "notes\x00.md",
	} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			for operation, call := range storeOperations(store, name) {
				t.Run(operation, func(t *testing.T) {
					if err := call(t.Context()); !errors.Is(err, fs.ErrInvalid) {
						t.Fatalf("error = %v, want fs.ErrInvalid", err)
					}
				})
			}
		})
	}
	for _, name := range []string{"", " \t\n"} {
		for operation, call := range storeOperations(store, name) {
			if operation == "list" || operation == "create directory" {
				continue
			}
			if err := call(t.Context()); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("%s(%q) = %v, want fs.ErrInvalid", operation, name, err)
			}
		}
	}
	if err := store.Write(t.Context(), "notes..md", "valid"); err != nil {
		t.Fatalf("dots within a file name must be allowed: %v", err)
	}
}

func TestFileSystemStore_RejectsUnexpectedFileTypes(t *testing.T) {
	store, root := newStore(t)
	ctx := t.Context()
	if err := store.CreateDirectory(ctx, "directory"); err != nil {
		t.Fatal(err)
	}
	for operation, call := range storeOperations(store, "directory") {
		if operation == "list" || operation == "create directory" {
			continue
		}
		if err := call(ctx); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("%s(directory) = %v, want fs.ErrInvalid", operation, err)
		}
	}
	if info, err := os.Stat(filepath.Join(root, "directory")); err != nil || !info.IsDir() {
		t.Fatalf("directory was removed: %v", err)
	}
	if err := store.Write(ctx, "file", "content"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(ctx, "file"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("List(file) = %v, want fs.ErrInvalid", err)
	}
	if err := store.CreateDirectory(ctx, "file/nested"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("CreateDirectory(file/nested) = %v, want fs.ErrInvalid", err)
	}
}

func TestFileSystemStore_RejectsSymbolicLinks(t *testing.T) {
	store, root := newStore(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), "inside.md", "inside"); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"outside.md":     secret,
		"linked-dir":     outside,
		"inside-link.md": filepath.Join(root, "inside.md"),
		"dangling.md":    filepath.Join(outside, "missing"),
	} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symbolic links are unavailable: %v", err)
			}
			t.Fatal(err)
		}
	}
	for _, name := range []string{"outside.md", "inside-link.md", "dangling.md", "linked-dir/secret.md"} {
		for operation, call := range storeOperations(store, name) {
			if err := call(t.Context()); !errors.Is(err, fs.ErrInvalid) {
				t.Fatalf("%s(%q) = %v, want fs.ErrInvalid", operation, name, err)
			}
		}
	}
	names, err := store.List(t.Context(), "")
	if err != nil || !slices.Equal(names, []string{"inside.md"}) {
		t.Fatalf("List() = %v, %v, want only inside.md", names, err)
	}
	content, err := os.ReadFile(secret)
	if err != nil || string(content) != "original" {
		t.Fatalf("outside content = %q, %v", content, err)
	}
}

func TestFileSystemStore_CancelledContext(t *testing.T) {
	store, _ := newStore(t)
	if err := store.Write(t.Context(), "notes.md", "original"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for operation, call := range storeOperations(store, "notes.md") {
		if err := call(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s error = %v, want context.Canceled", operation, err)
		}
	}
	content, err := store.Read(t.Context(), "notes.md")
	if err != nil || content != "original" {
		t.Fatalf("content after cancelled operations = %q, %v", content, err)
	}
}

func TestFileSystemStore_Close(t *testing.T) {
	store, _ := newStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for operation, call := range storeOperations(store, "notes.md") {
		if err := call(t.Context()); !errors.Is(err, fs.ErrClosed) {
			t.Fatalf("%s error = %v, want fs.ErrClosed", operation, err)
		}
	}
}

func TestFileSystemStore_ConcurrentWrites(t *testing.T) {
	store, _ := newStore(t)
	payloads := make([]string, 16)
	for i := range payloads {
		payloads[i] = strings.Repeat(fmt.Sprintf("writer-%d\n", i), 512)
	}
	var wg sync.WaitGroup
	for i, content := range payloads {
		wg.Go(func() {
			if err := store.Write(t.Context(), "shared.md", content); err != nil {
				t.Error(err)
			}
			name := fmt.Sprintf("work/%d.md", i)
			if err := store.Write(t.Context(), name, content); err != nil {
				t.Error(err)
			}
			got, err := store.Read(t.Context(), name)
			if err != nil || got != content {
				t.Errorf("Read(%q) = %q, %v", name, got, err)
			}
		})
	}
	wg.Wait()
	got, err := store.Read(t.Context(), "shared.md")
	if err != nil || !slices.Contains(payloads, got) {
		t.Fatalf("shared file contains a partial write: %v", err)
	}
	names, err := store.List(t.Context(), "work")
	if err != nil || len(names) != len(payloads) {
		t.Fatalf("List(work) returned %d files, %v", len(names), err)
	}
}
