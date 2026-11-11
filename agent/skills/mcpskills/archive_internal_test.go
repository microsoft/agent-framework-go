// Copyright (c) Microsoft. All rights reserved.

package mcpskills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
)

const extractedSkillMD = `---
name: archived-skill
description: A skill delivered as an archive.
---
# Archived Skill

Body from the archive.`

func newArchiveExtractor(t *testing.T) *Source {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return &Source{
		archiveRoot:         root,
		maxFileCount:        20,
		maxUncompressedSize: 1 << 20,
		logger:              slog.New(slog.DiscardHandler),
	}
}

func extractorZIP(t *testing.T, files ...[2]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range files {
		entry, err := writer.Create(file[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, file[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestArchiveFormat_TarSignalsAreUnsupported(t *testing.T) {
	for _, test := range []struct {
		data      []byte
		mediaType string
		uri       string
	}{
		{data: []byte{0x1f, 0x8b}, mediaType: "application/zip", uri: "skill://archive.zip"},
		{mediaType: "application/x-tar"},
		{uri: "skill://archive.tar"},
		{uri: "skill://archive.tgz"},
	} {
		if isZIP(test.data, test.mediaType, test.uri) {
			t.Fatalf("isZIP(%x, %q, %q) = true", test.data, test.mediaType, test.uri)
		}
	}
}

func TestExtractArchive_ZIPSlipEntry_DoesNotEscapeTargetDirectory(t *testing.T) {
	source := newArchiveExtractor(t)
	archive := extractorZIP(t, [2]string{"../escaped.txt", "pwned"}, [2]string{"SKILL.md", extractedSkillMD})

	if err := source.extractArchive(t.Context(), "skill", archive, "application/zip", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := source.archiveRoot.Stat("escaped.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("escaped file Stat() = %v, want not found", err)
	}
	if _, err := source.archiveRoot.Stat("skill/SKILL.md"); err != nil {
		t.Fatal(err)
	}
}

func TestExtractArchive_CaseVariantZIPSlipEntry_DoesNotEscapeTargetDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a case-sensitive filesystem")
	}
	source := newArchiveExtractor(t)
	archive := extractorZIP(t, [2]string{"../skill/escaped.txt", "pwned"}, [2]string{"SKILL.md", extractedSkillMD})

	if err := source.extractArchive(t.Context(), "Skill", archive, "application/zip", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := source.archiveRoot.Stat("skill/escaped.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("case-variant escaped file Stat() = %v, want not found", err)
	}
	if _, err := source.archiveRoot.Stat("Skill/SKILL.md"); err != nil {
		t.Fatal(err)
	}
}

func TestExtractArchive_ExceedsDefaultFileCount_ReturnsError(t *testing.T) {
	source := newArchiveExtractor(t)
	files := make([][2]string, 21)
	for i := range files {
		files[i] = [2]string{fmt.Sprintf("file%d.txt", i), "x"}
	}
	archive := extractorZIP(t, files...)

	err := source.extractArchive(t.Context(), "skill", archive, "application/zip", "")
	if err == nil || !strings.Contains(err.Error(), "maximum file count (20)") {
		t.Fatalf("extractArchive() error = %v, want file-count limit", err)
	}
}

func TestExtractArchive_ExceedsDefaultUncompressedSize_ReturnsError(t *testing.T) {
	source := newArchiveExtractor(t)
	archive := extractorZIP(t, [2]string{"SKILL.md", strings.Repeat("x", 1<<20+1)})

	err := source.extractArchive(t.Context(), "skill", archive, "application/zip", "")
	if err == nil || !strings.Contains(err.Error(), "maximum uncompressed size") {
		t.Fatalf("extractArchive() error = %v, want uncompressed-size limit", err)
	}
}

func TestExtractArchive_UnsupportedFormat_DoesNotCreateTarget(t *testing.T) {
	source := newArchiveExtractor(t)

	err := source.extractArchive(t.Context(), "skill", nil, "", "")
	if !errors.Is(err, errUnsupportedArchiveFormat) || !strings.Contains(err.Error(), "use ZIP instead") {
		t.Fatalf("extractArchive() error = %v, want unsupported-format error", err)
	}
	if _, err := source.archiveRoot.Stat("skill"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("target Stat() = %v, want not found", err)
	}
}

func TestExtractArchive_WithinDefaultLimits_Succeeds(t *testing.T) {
	source := newArchiveExtractor(t)
	archive := extractorZIP(t, [2]string{"SKILL.md", extractedSkillMD}, [2]string{"reference.md", "Some reference content."})

	if err := source.extractArchive(t.Context(), "skill", archive, "application/zip", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"skill/SKILL.md", "skill/reference.md"} {
		if _, err := source.archiveRoot.Stat(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtractArchive_RaisedLimits_AllowLargerArchive(t *testing.T) {
	source := newArchiveExtractor(t)
	files := make([][2]string, 21)
	for i := range files {
		files[i] = [2]string{fmt.Sprintf("file%d.txt", i), "x"}
	}
	archive := extractorZIP(t, files...)
	source.maxFileCount = len(files) + 1

	if err := source.extractArchive(t.Context(), "skill", archive, "application/zip", ""); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(source.archiveRoot.FS(), "skill")
	if err != nil || len(entries) != len(files) {
		t.Fatalf("extracted files = %d, %v, want %d", len(entries), err, len(files))
	}
}
