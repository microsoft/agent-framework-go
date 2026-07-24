// Copyright (c) Microsoft. All rights reserved.

package filestore_test

import (
	"context"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/harness/filestore"
)

func TestInMemoryStore_WriteReadDeleteAndExists(t *testing.T) {
	store := filestore.NewInMemoryStore()
	ctx := context.Background()

	if err := store.Write(ctx, `notes\plan.md`, "draft"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if exists, err := store.FileExists(ctx, "notes/plan.md"); err != nil || !exists {
		t.Fatalf("FileExists() = %v, %v, want true, nil", exists, err)
	}
	if got, found, err := store.Read(ctx, "notes/plan.md"); err != nil || !found || got != "draft" {
		t.Fatalf("Read() = %q, %v, %v", got, found, err)
	}
	if deleted, err := store.Delete(ctx, "notes/plan.md"); err != nil || !deleted {
		t.Fatalf("Delete() = %v, %v, want true, nil", deleted, err)
	}
	if _, found, err := store.Read(ctx, "notes/plan.md"); err != nil || found {
		t.Fatalf("Read() after delete found = %v, err = %v", found, err)
	}
}

func TestInMemoryStore_ListChildrenAndSearch(t *testing.T) {
	store := filestore.NewInMemoryStore()
	ctx := context.Background()
	for path, content := range map[string]string{
		"root/a.txt":     "hello world\nsecond line",
		"root/sub/b.txt": "hello again",
		"root/c.md":      "markdown only",
	} {
		if err := store.Write(ctx, path, content); err != nil {
			t.Fatalf("Write(%q) error = %v", path, err)
		}
	}

	children, err := store.ListChildren(ctx, "root")
	if err != nil {
		t.Fatalf("ListChildren() error = %v", err)
	}
	wantChildren := []filestore.Entry{
		{Name: "sub", Type: filestore.EntryTypeDirectory},
		{Name: "a.txt", Type: filestore.EntryTypeFile},
		{Name: "c.md", Type: filestore.EntryTypeFile},
	}
	if !slices.Equal(children, wantChildren) {
		t.Fatalf("ListChildren() = %#v, want %#v", children, wantChildren)
	}

	results, err := store.Search(ctx, "root", "hello", "*.txt", false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 || results[0].FileName != "a.txt" || len(results[0].MatchingLines) != 1 {
		t.Fatalf("Search(non-recursive) = %#v", results)
	}

	results, err = store.Search(ctx, "root", "hello", "**/*.txt", true)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got := []string{results[0].FileName, results[1].FileName}; !slices.Equal(got, []string{"a.txt", "sub/b.txt"}) {
		t.Fatalf("Search(recursive) files = %v", got)
	}
}

func TestInMemoryStore_RejectsInvalidPaths(t *testing.T) {
	store := filestore.NewInMemoryStore()
	ctx := context.Background()

	if err := store.Write(ctx, "../escape.txt", "nope"); err == nil {
		t.Fatal("expected traversal path error")
	}
	if _, _, err := store.Read(ctx, "/absolute.txt"); err == nil {
		t.Fatal("expected absolute path error")
	}
}
