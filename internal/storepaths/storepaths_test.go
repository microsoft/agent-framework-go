// Copyright (c) Microsoft. All rights reserved.

package storepaths_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/microsoft/agent-framework-go/internal/storepaths"
)

func assertNormalized(t *testing.T, input, want string, directory bool) {
	t.Helper()
	got, err := storepaths.Normalize(input, directory)
	if err != nil || got != want {
		t.Fatalf("Normalize(%q, %v) = %q, %v, want %q", input, directory, got, err, want)
	}
}

func assertInvalid(t *testing.T, input string, directory bool) {
	t.Helper()
	if _, err := storepaths.Normalize(input, directory); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("Normalize(%q, %v) error = %v, want fs.ErrInvalid", input, directory, err)
	}
}

func TestNormalize_ValidPath(t *testing.T) {
	for _, path := range []string{"file.md", "folder/file.md", "a/b/c.txt"} {
		t.Run(path, func(t *testing.T) { assertNormalized(t, path, path, false) })
	}
}

func TestNormalize_Backslashes(t *testing.T) {
	for _, tc := range [][2]string{{`folder\file.md`, "folder/file.md"}, {`a\b\c.txt`, "a/b/c.txt"}} {
		t.Run(tc[0], func(t *testing.T) { assertNormalized(t, tc[0], tc[1], false) })
	}
}

func TestNormalize_ConsecutiveSeparators(t *testing.T) {
	for _, tc := range [][2]string{{"folder//file.md", "folder/file.md"}, {"a///b////c.txt", "a/b/c.txt"}} {
		t.Run(tc[0], func(t *testing.T) { assertNormalized(t, tc[0], tc[1], false) })
	}
}

func TestNormalize_TrailingSlash(t *testing.T) {
	assertNormalized(t, "file.md/", "file.md", false)
}

func TestNormalize_LeadingSlash(t *testing.T) {
	for _, path := range []string{"/file.md", "/folder/file.md/"} {
		t.Run(path, func(t *testing.T) { assertInvalid(t, path, false) })
	}
}

func TestNormalize_EmptyFile(t *testing.T) {
	assertInvalid(t, "", false)
}

func TestNormalize_WhitespaceFile(t *testing.T) {
	assertInvalid(t, "   ", false)
}

func TestNormalize_EmptyDirectory(t *testing.T) {
	assertNormalized(t, "", "", true)
}

func TestNormalize_Directory(t *testing.T) {
	for _, tc := range [][2]string{{"folder", "folder"}, {"a/b", "a/b"}, {`a\b/`, "a/b"}} {
		t.Run(tc[0], func(t *testing.T) { assertNormalized(t, tc[0], tc[1], true) })
	}
}

func TestNormalize_DirectoryTraversal(t *testing.T) {
	assertInvalid(t, "../folder", true)
}
