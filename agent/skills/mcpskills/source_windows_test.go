//go:build windows

// Copyright (c) Microsoft. All rights reserved.

package mcpskills_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/skills/mcpskills"
)

func TestSource_PreExtractionCleanupFailure_DoesNotDiscoverStaleSkill(t *testing.T) {
	root := extractionRoot(t)
	skillDirectory := filepath.Join(root, "archived-skill")
	if err := os.MkdirAll(skillDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"), []byte(archivedMD+"\nStale content."), 0o600); err != nil {
		t.Fatal(err)
	}
	lockedPath := filepath.Join(skillDirectory, "locked.txt")
	path, err := syscall.UTF16PtrFromString(lockedPath)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.CREATE_NEW, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.CloseHandle(handle); err != nil {
			t.Error(err)
		}
	})
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})
	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("failed cleanup discovered %d stale skills", len(got))
	}
	if _, err := os.Stat(lockedPath); err != nil {
		t.Fatalf("locked file unexpectedly disappeared: %v", err)
	}
	if got := server.count(archiveURI); got != 0 {
		t.Fatalf("cleanup failure still downloaded %d archives", got)
	}
}
