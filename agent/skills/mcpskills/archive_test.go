// Copyright (c) Microsoft. All rights reserved.

package mcpskills_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/mcpskills"
)

const (
	archiveURI = "skill://archives/archived-skill.zip"
	archivedMD = `---
name: archived-skill
description: A skill delivered as an archive.
---
# Archived Skill

Body from the archive.`
	staleSkillMD = `---
name: stale-skill
description: Left over from a previous session.
---
Old content.`
	skillAMD = `---
name: skill-a
description: Skill A.
---
Content A.`
	skillBMD = `---
name: skill-b
description: Skill B.
---
Content B.`
	sharedSkillMD = `---
name: shared-skill
description: Shared.
---
Content from server %s.`
	manyFileArchiveFileCount = 60
)

type archiveFile struct {
	name    string
	content string
}

func makeZIP(t *testing.T, files ...archiveFile) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range files {
		entry, err := writer.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, file.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func archiveEntry(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"type":        "archive",
		"description": "A skill delivered as an archive.",
		"url":         "skill://archives/" + name + ".zip",
	}
}

func setIndex(t *testing.T, server *testServer, entries ...map[string]any) {
	t.Helper()
	index, err := json.Marshal(map[string]any{"skills": entries})
	if err != nil {
		t.Fatal(err)
	}
	server.text(indexURI, string(index))
}

func digest(data []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

func extractionRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "archives")
}

func assertMissing(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(%q) = %v, want not exist", name, err)
	}
}

func readStaticResource(t *testing.T, skill *skills.Skill, name string) any {
	t.Helper()
	for _, resource := range skill.Resources {
		if resource.Name == name {
			result, err := resource.Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatalf("resource %q not found in %#v", name, skill.Resources)
	return nil
}

func manyFileZIP(t *testing.T) []byte {
	t.Helper()
	files := []archiveFile{{"SKILL.md", archivedMD}}
	for i := range manyFileArchiveFileCount - 1 {
		files = append(files, archiveFile{fmt.Sprintf("reference/file%d.txt", i), "x"})
	}
	return makeZIP(t, files...)
}

func writeStaleArchiveSkill(t *testing.T, root string) string {
	t.Helper()
	directory := filepath.Join(root, "stale-skill")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(staleSkillMD), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestSource_ArchiveContainerNames_AllowSafeDirectoryComponents(t *testing.T) {
	for _, name := range []string{"bundle.v1", "Bundle.V1", "bundle_v1", "bundle v1", strings.Repeat("b", 65)} {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t)
			entry := archiveEntry(name)
			entry["url"] = archiveURI
			setIndex(t, server, entry)
			server.blob(archiveURI, makeZIP(t,
				archiveFile{"skill-a/SKILL.md", skillAMD},
				archiveFile{"skill-a/reference.md", "Nested resource."},
			), "application/zip")
			source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

			skill := loadOne(t, source)
			if skill.Frontmatter.Name != "skill-a" {
				t.Fatalf("nested skill name = %q", skill.Frontmatter.Name)
			}
			if got := readStaticResource(t, skill, "reference.md"); got != "Nested resource." {
				t.Fatalf("nested resource = %#v", got)
			}
		})
	}
}

func TestSource_ArchiveContainerNames_RejectUnsafeDirectoryComponents(t *testing.T) {
	for _, name := range []string{"", " ", ".", "..", "...", " . ", "../outside", `..\outside`, "bundle/child", `bundle\child`, "/outside", "bundle\x00v1"} {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t)
			entry := archiveEntry(name)
			entry["url"] = archiveURI
			setIndex(t, server, entry)
			server.blob(archiveURI, makeZIP(t, archiveFile{"skill-a/SKILL.md", skillAMD}), "application/zip")
			root := extractionRoot(t)
			source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})

			if got := loadSkills(t, source); len(got) != 0 {
				t.Fatalf("unsafe container name returned %d skills", len(got))
			}
			if calls := server.count(archiveURI); calls != 0 {
				t.Fatalf("unsafe container downloaded %d archives", calls)
			}
			assertMissing(t, root)
		})
	}
}

func TestSource_ArchiveResourceRead_WaitsForRefreshAndCanCancel(t *testing.T) {
	for _, cancelRead := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel read=", cancelRead), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server := newTestServer(t)
				setIndex(t, server, archiveEntry("archived-skill"))
				server.blob(archiveURI, makeZIP(t,
					archiveFile{"SKILL.md", archivedMD}, archiveFile{"reference.md", "original"},
				), "application/zip")
				source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})
				skill := loadOne(t, source)
				if len(skill.Resources) != 1 {
					t.Fatalf("Resources = %#v, want reference.md", skill.Resources)
				}
				resource := skill.Resources[0]
				server.blob(archiveURI, makeZIP(t,
					archiveFile{"SKILL.md", archivedMD}, archiveFile{"reference.md", "updated"},
				), "application/zip")
				refreshStarted, release := make(chan struct{}), make(chan struct{})
				releaseRefresh := sync.OnceFunc(func() { close(release) })
				t.Cleanup(releaseRefresh)
				server.hook(func(ctx context.Context, uri string) error {
					if uri == archiveURI {
						close(refreshStarted)
						select {
						case <-release:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					return nil
				})
				refreshDone := make(chan error, 1)
				go func() {
					_, err := source.Skills(t.Context(), skills.SourceContext{})
					refreshDone <- err
				}()
				<-refreshStarted
				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)
				type readResult struct {
					value any
					err   error
				}
				readDone := make(chan readResult, 1)
				go func() {
					value, err := resource.Read(ctx)
					readDone <- readResult{value, err}
				}()
				synctest.Wait()
				select {
				case got := <-readDone:
					t.Fatalf("resource read completed during refresh: %#v, %v", got.value, got.err)
				default:
				}
				if cancelRead {
					cancel()
					got := <-readDone
					if !errors.Is(got.err, context.Canceled) || got.value != nil {
						t.Fatalf("canceled waiting read = %#v, %v", got.value, got.err)
					}
				}
				releaseRefresh()
				if err := <-refreshDone; err != nil {
					t.Fatal(err)
				}
				if !cancelRead {
					if got := <-readDone; got.err != nil || got.value != "updated" {
						t.Fatalf("read after refresh = %#v, %v, want updated", got.value, got.err)
					}
				}
			})
		})
	}
}

func TestSource_ArchiveResourceRead_AfterCloseReturnsClosed(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t,
		archiveFile{"SKILL.md", archivedMD}, archiveFile{"reference.md", "content"},
	), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})
	skill := loadOne(t, source)
	if len(skill.Resources) != 1 {
		t.Fatalf("Resources = %#v, want reference.md", skill.Resources)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	value, err := skill.Resources[0].Read(t.Context())
	if !errors.Is(err, fs.ErrClosed) || value != nil {
		t.Fatalf("Read() after Close() = %#v, %v, want closed", value, err)
	}
}

func TestSource_ZIPArchive_DiscoversSkill(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t,
		archiveFile{"SKILL.md", archivedMD},
		archiveFile{"reference.md", "Some reference content."},
	), "application/zip")
	root := extractionRoot(t)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root}))
	if skill.Frontmatter.Name != "archived-skill" || skill.Frontmatter.Description != "A skill delivered as an archive." {
		t.Fatalf("Frontmatter = %#v", skill.Frontmatter)
	}
	if got := content(t, skill); !strings.Contains(got, "Body from the archive.") {
		t.Fatalf("GetContent() = %q", got)
	}
	if got := readStaticResource(t, skill, "reference.md"); got != "Some reference content." {
		t.Fatalf("Read() = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "archived-skill", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSource_ArchiveScripts_AreOnlyReadableResources(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t,
		archiveFile{"SKILL.md", archivedMD},
		archiveFile{"scripts/run.py", "print('hello')"},
	), "application/zip")
	source := newSource(t, server, mcpskills.Config{
		ArchiveDirectory:          extractionRoot(t),
		ArchiveResourceExtensions: []string{".md", ".py"},
	})
	skill := loadOne(t, source)
	if len(skill.Scripts) != 0 || skill.GetScript != nil {
		t.Fatalf("archive exposed executable scripts or lazy script lookup: %#v", skill.Scripts)
	}
	if got := readStaticResource(t, skill, "scripts/run.py"); got != "print('hello')" {
		t.Fatalf("script resource = %#v", got)
	}
	if strings.Contains(content(t, skill), "<available_scripts>") {
		t.Fatal("archive advertised executable scripts")
	}
}

func TestSource_SeparateSources_DoNotCollide(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		t.Run(fmt.Sprint("temporary=", temporary), func(t *testing.T) {
			var sources [2]*mcpskills.Source
			for i, version := range []string{"A", "B"} {
				server := newTestServer(t)
				setIndex(t, server, archiveEntry("archived-skill"))
				server.blob(archiveURI, makeZIP(t,
					archiveFile{"SKILL.md", archivedMD + "\nContent from server " + version + "."},
					archiveFile{"version.txt", version},
				), "application/zip")
				config := mcpskills.Config{}
				if !temporary {
					config.ArchiveDirectory = extractionRoot(t)
				}
				sources[i] = newSource(t, server, config)
			}
			first := loadOne(t, sources[0])
			second := loadOne(t, sources[1])
			if !strings.Contains(content(t, first), "Content from server A.") || !strings.Contains(content(t, second), "Content from server B.") {
				t.Fatal("separate sources mixed skill content")
			}
			if readStaticResource(t, first, "version.txt") != "A" || readStaticResource(t, second, "version.txt") != "B" {
				t.Fatal("separate sources shared extracted resources")
			}
		})
	}
}

func TestSource_ArchiveRefresh_ReplacesContentAndRetainsInstructionSnapshots(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD + "\nVersion one."}), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})
	before := loadOne(t, source)
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD + "\nVersion two."}), "application/zip")

	results := make(chan []*skills.Skill, 10)
	failures := make(chan error, 10)
	for range 10 {
		go func() {
			result, err := source.Skills(t.Context(), skills.SourceContext{})
			results <- result
			failures <- err
		}()
	}
	if !strings.Contains(content(t, before), "Version one.") {
		t.Fatal("refresh replaced the earlier instruction snapshot")
	}
	for range 10 {
		result, err := <-results, <-failures
		if err != nil || len(result) != 1 {
			t.Fatalf("Skills() = %#v, %v", result, err)
		}
		if got := content(t, result[0]); !strings.Contains(got, "Version two.") || strings.Contains(got, "Version one.") {
			t.Fatalf("refreshed content = %q", got)
		}
	}
}

func TestSource_FirstDiscovery_PrunesLeftovers(t *testing.T) {
	root := extractionRoot(t)
	stale := filepath.Join(root, "stale-skill")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "SKILL.md"), []byte(archivedMD), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("not a skill directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	_ = loadOne(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root}))
	assertMissing(t, stale)
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Fatalf("pruning removed an unrelated root file: %v", err)
	}
}

func TestSource_NoLongerAdvertisedArchive_IsPruned(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("skill-a"), archiveEntry("skill-b"))
	for _, name := range []string{"skill-a", "skill-b"} {
		server.blob("skill://archives/"+name+".zip", makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(archivedMD, "archived-skill", name)}), "application/zip")
	}
	root := extractionRoot(t)
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})
	if got := loadSkills(t, source); len(got) != 2 {
		t.Fatalf("initial discovery returned %d skills", len(got))
	}
	setIndex(t, server, archiveEntry("skill-a"))
	skill := loadOne(t, source)
	if skill.Frontmatter.Name != "skill-a" {
		t.Fatalf("remaining skill = %q", skill.Frontmatter.Name)
	}
	assertMissing(t, filepath.Join(root, "skill-b"))
}

func TestSource_EmptyAdvertisement_PrunesArchives(t *testing.T) {
	for _, mode := range []string{"empty", "no index", "malformed index", "template only", "invalid archive"} {
		t.Run(mode, func(t *testing.T) {
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
			root := extractionRoot(t)
			source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})
			_ = loadOne(t, source)
			switch mode {
			case "empty":
				setIndex(t, server)
			case "no index":
				server.fail(indexURI, errors.New("index unavailable"))
			case "malformed index":
				server.text(indexURI, "{")
			case "template only":
				server.text(indexURI, `{"skills":[{"type":"mcp-resource-template","url":"skill://{name}/SKILL.md"}]}`)
			case "invalid archive":
				entry := archiveEntry("archived-skill")
				delete(entry, "url")
				setIndex(t, server, entry)
			}
			if got := loadSkills(t, source); len(got) != 0 {
				t.Fatalf("empty advertisement returned %d skills", len(got))
			}
			assertMissing(t, filepath.Join(root, "archived-skill"))
		})
	}
}

func TestSource_ConcurrentDiscoveries_KeepAllArchiveContent(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("skill-a"), archiveEntry("skill-b"))
	for _, name := range []string{"skill-a", "skill-b"} {
		server.blob("skill://archives/"+name+".zip", makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(archivedMD, "archived-skill", name) + "\n" + name}), "application/zip")
	}
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})
	type result struct {
		skills []*skills.Skill
		err    error
	}
	done := make(chan result, 20)
	start := make(chan struct{})
	for range 20 {
		go func() {
			<-start
			skills, err := source.Skills(t.Context(), skills.SourceContext{})
			done <- result{skills, err}
		}()
	}
	close(start)
	for range 20 {
		got := <-done
		if got.err != nil || len(got.skills) != 2 {
			t.Fatalf("Skills() = %#v, %v", got.skills, got.err)
		}
		names := make([]string, 0, 2)
		for _, skill := range got.skills {
			names = append(names, skill.Frontmatter.Name)
			if !strings.Contains(content(t, skill), skill.Frontmatter.Name) {
				t.Fatalf("incomplete content for %q", skill.Frontmatter.Name)
			}
		}
		if !slices.Equal(names, []string{"skill-a", "skill-b"}) {
			t.Fatalf("skill order = %q", names)
		}
	}
}

func TestSource_ArchiveWaiter_CanCancel(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})
	started, release, secondIndex := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var indexReads atomic.Int32
	server.hook(func(ctx context.Context, uri string) error {
		if uri == indexURI && indexReads.Add(1) == 2 {
			close(secondIndex)
		}
		if uri == archiveURI {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	first := make(chan error, 1)
	go func() {
		_, err := source.Skills(t.Context(), skills.SourceContext{})
		first <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	second := make(chan error, 1)
	go func() {
		_, err := source.Skills(ctx, skills.SourceContext{})
		second <- err
	}()
	<-secondIndex
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting Skills() error = %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if got := server.count(archiveURI); got != 1 {
		t.Fatalf("archive downloads = %d, want 1", got)
	}
}

func TestSource_ArchiveDigest_AcceptsOmittedNullAndMatching(t *testing.T) {
	for _, mode := range []string{"omitted", "null", "matching"} {
		t.Run(mode, func(t *testing.T) {
			server := newTestServer(t)
			archive := makeZIP(t, archiveFile{"SKILL.md", archivedMD}, archiveFile{"reference.md", "Verified resource."})
			entry := archiveEntry("archived-skill")
			switch mode {
			case "null":
				entry["digest"] = nil
			case "matching":
				entry["digest"] = digest(archive)
			}
			setIndex(t, server, entry)
			server.blob(archiveURI, archive, "application/zip")
			skill := loadOne(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)}))
			if !strings.Contains(content(t, skill), "Body from the archive.") || readStaticResource(t, skill, "reference.md") != "Verified resource." {
				t.Fatal("verified archive did not retain content and resources")
			}
		})
	}
}

func TestSource_RejectedArchiveDigest_SkipsBeforeExtractionAndLogs(t *testing.T) {
	rejected := []string{
		"", " ", strings.Repeat("0", 64),
		"sha256:" + strings.Repeat("a", 63),
		"sha256:" + strings.Repeat("a", 65),
		"sha256:" + strings.Repeat("g", 64),
		"sha256:" + strings.Repeat("A", 64),
		"SHA256:" + strings.Repeat("a", 64),
		" sha256:" + strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("a", 64) + "\n",
		"sha512:" + strings.Repeat("a", 128),
		"sha256:" + strings.Repeat("0", 64),
	}
	for i, rejectedDigest := range rejected {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			server := newTestServer(t)
			entry := archiveEntry("archived-skill")
			entry["digest"] = rejectedDigest
			setIndex(t, server, entry)
			server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
			var logs bytes.Buffer
			root := extractionRoot(t)
			source := newSource(t, server, mcpskills.Config{
				ArchiveDirectory: root,
				Logger:           slog.New(slog.NewTextHandler(&logs, nil)),
			})
			if got := loadSkills(t, source); len(got) != 0 {
				t.Fatalf("rejected digest returned %d skills", len(got))
			}
			assertMissing(t, filepath.Join(root, "archived-skill"))
			if got := logs.String(); strings.Count(got, "digest") != 1 || strings.Count(got, "level=WARN") != 1 || strings.Contains(got, "skill://") {
				t.Fatalf("digest diagnostics = %q", got)
			}
		})
	}
}

func TestSource_DigestOfDifferentBytes_IsRejected(t *testing.T) {
	for _, mode := range []string{"original", "base64", "skill-md"} {
		t.Run(mode, func(t *testing.T) {
			original := makeZIP(t, archiveFile{"SKILL.md", archivedMD})
			tampered := strings.ReplaceAll(archivedMD, "Body from the archive.", "Substituted instructions.")
			archive := makeZIP(t, archiveFile{"SKILL.md", tampered})
			toHash := original
			switch mode {
			case "base64":
				toHash = []byte(base64.StdEncoding.EncodeToString(archive))
			case "skill-md":
				toHash = []byte(tampered)
			}
			server := newTestServer(t)
			entry := archiveEntry("archived-skill")
			entry["digest"] = digest(toHash)
			setIndex(t, server, entry)
			server.blob(archiveURI, archive, "application/zip")
			root := extractionRoot(t)
			if got := loadSkills(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root})); len(got) != 0 {
				t.Fatalf("different-byte digest returned %d skills", len(got))
			}
			assertMissing(t, filepath.Join(root, "archived-skill"))
		})
	}
}

func TestSource_RejectedDigest_PreservesValidSibling(t *testing.T) {
	server := newTestServer(t)
	rejected := archiveEntry("archived-skill")
	rejected["digest"] = ""
	valid := archiveEntry("skill-a")
	validArchive := makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(archivedMD, "archived-skill", "skill-a")})
	valid["digest"] = digest(validArchive)
	setIndex(t, server, rejected, valid)
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	server.blob("skill://archives/skill-a.zip", validArchive, "application/zip")
	root := extractionRoot(t)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root}))
	if skill.Frontmatter.Name != "skill-a" {
		t.Fatalf("remaining skill = %q", skill.Frontmatter.Name)
	}
	assertMissing(t, filepath.Join(root, "archived-skill"))
}

func TestSource_NonStringDigest_RejectsEntireIndex(t *testing.T) {
	for _, invalid := range []any{123, false, map[string]any{}, []any{}} {
		t.Run(fmt.Sprintf("%T", invalid), func(t *testing.T) {
			server := newTestServer(t)
			entry := archiveEntry("archived-skill")
			entry["digest"] = invalid
			setIndex(t, server, entry)
			server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
			root := extractionRoot(t)
			if got := loadSkills(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root})); len(got) != 0 {
				t.Fatalf("invalid digest type returned %d skills", len(got))
			}
			assertMissing(t, root)
			if got := server.count(archiveURI); got != 0 {
				t.Fatalf("invalid index downloaded %d archives", got)
			}
		})
	}
}

func TestSource_MatchingDigest_DoesNotBypassArchiveGuards(t *testing.T) {
	for _, failure := range []string{"invalid ZIP", "gzip", "file count", "uncompressed size", "download size"} {
		t.Run(failure, func(t *testing.T) {
			archive := makeZIP(t, archiveFile{"SKILL.md", archivedMD}, archiveFile{"reference.md", "Reference."})
			config := mcpskills.Config{ArchiveDirectory: extractionRoot(t)}
			switch failure {
			case "invalid ZIP":
				archive = []byte{'P', 'K', 3, 4}
			case "gzip":
				archive = []byte{0x1f, 0x8b}
			case "file count":
				config.ArchiveMaxFileCount = new(1)
			case "uncompressed size":
				config.ArchiveMaxUncompressedSizeBytes = new(int64(1))
			case "download size":
				config.ArchiveMaxSizeBytes = new(int64(1))
			}
			server := newTestServer(t)
			entry := archiveEntry("archived-skill")
			entry["digest"] = digest(archive)
			setIndex(t, server, entry)
			server.blob(archiveURI, archive, "application/zip")
			if got := loadSkills(t, newSource(t, server, config)); len(got) != 0 {
				t.Fatalf("matching digest bypassed %s guard", failure)
			}
			assertMissing(t, filepath.Join(config.ArchiveDirectory, "archived-skill"))
		})
	}
}

func TestSource_DigestMismatchOnRefresh_RemovesPreviousExtraction(t *testing.T) {
	server := newTestServer(t)
	original := makeZIP(t, archiveFile{"SKILL.md", archivedMD})
	entry := archiveEntry("archived-skill")
	entry["digest"] = digest(original)
	setIndex(t, server, entry)
	server.blob(archiveURI, original, "application/zip")
	root := extractionRoot(t)
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})
	_ = loadOne(t, source)
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD + "\nSubstituted content."}), "application/zip")
	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("mismatched refresh returned %d skills", len(got))
	}
	assertMissing(t, filepath.Join(root, "archived-skill"))
	server.blob(archiveURI, original, "application/zip")
	_ = loadOne(t, source)
}

func TestSource_ZIPSlipEntries_DoNotEscapeSkillDirectory(t *testing.T) {
	for _, malicious := range []string{
		"../escaped.txt", "../Archived-skill/escaped.txt", "../../escaped.txt",
		`..\escaped.txt`, "/escaped.txt", `C:\escaped.txt`, "reference.md:stream",
	} {
		t.Run(malicious, func(t *testing.T) {
			server := newTestServer(t)
			archive := makeZIP(t, archiveFile{malicious, "outside"}, archiveFile{"SKILL.md", archivedMD})
			entry := archiveEntry("archived-skill")
			entry["digest"] = digest(archive)
			setIndex(t, server, entry)
			server.blob(archiveURI, archive, "application/zip")
			root := extractionRoot(t)
			_ = loadOne(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root}))
			assertMissing(t, filepath.Join(root, "escaped.txt"))
			assertMissing(t, filepath.Join(root, "Archived-skill", "escaped.txt"))
			entries, err := os.ReadDir(filepath.Join(root, "archived-skill"))
			if err != nil || len(entries) != 1 || entries[0].Name() != "SKILL.md" {
				t.Fatalf("extracted files = %#v, %v, want only SKILL.md", entries, err)
			}
		})
	}
}

func TestSource_ArchiveLimits_ExactThresholds(t *testing.T) {
	for _, count := range []int{20, 21} {
		t.Run(fmt.Sprint("file count ", count), func(t *testing.T) {
			files := []archiveFile{{"SKILL.md", archivedMD}}
			for i := 1; i < count; i++ {
				files = append(files, archiveFile{fmt.Sprintf("file%d.txt", i), "x"})
			}
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			server.blob(archiveURI, makeZIP(t, files...), "application/zip")
			root := extractionRoot(t)
			got := loadSkills(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root}))
			if (len(got) == 1) != (count == 20) {
				t.Fatalf("file count %d returned %d skills", count, len(got))
			}
			if count == 21 {
				assertMissing(t, filepath.Join(root, "archived-skill"))
			}
		})
	}
	for _, size := range []int{1 << 20, 1<<20 + 1} {
		t.Run(fmt.Sprint("uncompressed size ", size), func(t *testing.T) {
			body := archivedMD + strings.Repeat("x", size-len(archivedMD))
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", body}), "application/zip")
			root := extractionRoot(t)
			got := loadSkills(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root}))
			if (len(got) == 1) != (size == 1<<20) {
				t.Fatalf("uncompressed size %d returned %d skills", size, len(got))
			}
			if size > 1<<20 {
				assertMissing(t, filepath.Join(root, "archived-skill"))
			}
		})
	}
	archive := makeZIP(t, archiveFile{"SKILL.md", archivedMD})
	for _, limit := range []int64{0, int64(len(archive) - 1), int64(len(archive))} {
		t.Run(fmt.Sprint("download size ", limit), func(t *testing.T) {
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			server.blob(archiveURI, archive, "application/zip")
			got := loadSkills(t, newSource(t, server, mcpskills.Config{
				ArchiveDirectory:    extractionRoot(t),
				ArchiveMaxSizeBytes: new(limit),
			}))
			if (len(got) == 1) != (limit == int64(len(archive))) {
				t.Fatalf("download limit %d returned %d skills", limit, len(got))
			}
		})
	}
}

func TestSource_RaisedArchiveLimits_AllowLargerArchives(t *testing.T) {
	files := []archiveFile{{"SKILL.md", archivedMD}}
	for i := range 60 {
		files = append(files, archiveFile{fmt.Sprintf("file%d.txt", i), "x"})
	}
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, files...), "application/zip")
	skill := loadOne(t, newSource(t, server, mcpskills.Config{
		ArchiveDirectory:                extractionRoot(t),
		ArchiveMaxFileCount:             new(len(files)),
		ArchiveMaxUncompressedSizeBytes: new(int64(math.MaxInt64)),
	}))
	if len(skill.Resources) != 60 {
		t.Fatalf("resources = %d, want 60", len(skill.Resources))
	}
}

func TestSource_DefaultDownloadLimit_ExactThreshold(t *testing.T) {
	build := func(payload string) []byte {
		t.Helper()
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		for _, file := range []archiveFile{{"SKILL.md", archivedMD}, {"reference.txt", payload}} {
			entry, err := writer.CreateHeader(&zip.FileHeader{Name: file.name, Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(entry, file.content); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	overhead := len(build(""))
	for _, size := range []int{1 << 20, 1<<20 + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			archive := build(strings.Repeat("x", size-overhead))
			if len(archive) != size {
				t.Fatalf("archive size = %d, want exact threshold %d", len(archive), size)
			}
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			server.blob(archiveURI, archive, "application/zip")
			root := extractionRoot(t)
			got := loadSkills(t, newSource(t, server, mcpskills.Config{
				ArchiveDirectory:                root,
				ArchiveMaxUncompressedSizeBytes: new(int64(2 << 20)),
			}))
			if (len(got) == 1) != (size == 1<<20) {
				t.Fatalf("%d-byte archive returned %d skills", size, len(got))
			}
			if size > 1<<20 {
				assertMissing(t, filepath.Join(root, "archived-skill"))
			}
		})
	}
}

func TestSource_UncompressedBudget_IsSharedAcrossFiles(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t,
		archiveFile{"SKILL.md", archivedMD + strings.Repeat("x", 600*1024-len(archivedMD))},
		archiveFile{"reference.txt", strings.Repeat("y", 600*1024)},
	), "application/zip")
	root := extractionRoot(t)
	if got := loadSkills(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root})); len(got) != 0 {
		t.Fatalf("combined uncompressed budget was bypassed: %d skills", len(got))
	}
	assertMissing(t, filepath.Join(root, "archived-skill"))
}

func TestSource_ZeroArchiveLimits_DisallowFiles(t *testing.T) {
	for _, limit := range []string{"file count", "uncompressed size"} {
		t.Run(limit, func(t *testing.T) {
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
			config := mcpskills.Config{ArchiveDirectory: extractionRoot(t)}
			if limit == "file count" {
				config.ArchiveMaxFileCount = new(0)
			} else {
				config.ArchiveMaxUncompressedSizeBytes = new(int64(0))
			}
			if got := loadSkills(t, newSource(t, server, config)); len(got) != 0 {
				t.Fatalf("zero %s limit returned %d skills", limit, len(got))
			}
			assertMissing(t, filepath.Join(config.ArchiveDirectory, "archived-skill"))
		})
	}
}

func TestSource_InvalidArchiveEntries_AreSkipped(t *testing.T) {
	for _, invalid := range []string{"missing name", "invalid name", "missing URL", "unreadable", "text only", "empty blob", "unknown format", "gzip"} {
		t.Run(invalid, func(t *testing.T) {
			server := newTestServer(t)
			entry := archiveEntry("archived-skill")
			switch invalid {
			case "missing name":
				delete(entry, "name")
			case "invalid name":
				entry["name"] = "../outside"
			case "missing URL":
				delete(entry, "url")
			}
			setIndex(t, server, entry)
			switch invalid {
			case "text only":
				server.text(archiveURI, "not binary")
			case "empty blob":
				server.blob(archiveURI, []byte{}, "application/zip")
			case "unknown format":
				entry["url"] = "skill://archives/archive.unknown"
				setIndex(t, server, entry)
				server.blob("skill://archives/archive.unknown", []byte("unknown"), "application/octet-stream")
			case "gzip":
				server.blob(archiveURI, []byte{0x1f, 0x8b}, "application/zip")
			}
			root := extractionRoot(t)
			if got := loadSkills(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: root})); len(got) != 0 {
				t.Fatalf("invalid %s returned %d skills", invalid, len(got))
			}
			assertMissing(t, filepath.Join(root, "archived-skill"))
		})
	}
}

func TestSource_ArchiveResourceConfiguration_IsCopied(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t,
		archiveFile{"SKILL.md", archivedMD},
		archiveFile{"root.txt", "root"},
		archiveFile{"nested/child.txt", "child"},
	), "application/zip")
	extensions := []string{".txt"}
	depth, count := 1, 3
	source := newSource(t, server, mcpskills.Config{
		ArchiveDirectory:           extractionRoot(t),
		ArchiveResourceExtensions:  extensions,
		ArchiveResourceSearchDepth: &depth,
		ArchiveMaxFileCount:        &count,
	})
	extensions[0], depth, count = ".unknown", 2, 0
	skill := loadOne(t, source)
	if len(skill.Resources) != 1 || skill.Resources[0].Name != "root.txt" {
		t.Fatalf("resources = %#v, configuration changed after construction", skill.Resources)
	}
}

func TestSource_EmptyResourceExtensions_DisablesDiscovery(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}, archiveFile{"reference.md", "reference"}), "application/zip")
	skill := loadOne(t, newSource(t, server, mcpskills.Config{
		ArchiveDirectory:          extractionRoot(t),
		ArchiveResourceExtensions: []string{},
	}))
	if len(skill.Resources) != 0 || len(skill.Scripts) != 0 {
		t.Fatalf("empty extension configuration exposed resources/scripts: %#v", skill)
	}
}

func TestSource_ArchiveDownloadCancellation_Propagates(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	root := extractionRoot(t)
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})
	started := make(chan struct{})
	server.hook(func(ctx context.Context, uri string) error {
		if uri == archiveURI {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := source.Skills(ctx, skills.SourceContext{})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("archive cancellation error = %v", err)
	}
	assertMissing(t, filepath.Join(root, "archived-skill"))
}

func TestSource_ArchiveFrontmatter_RejectsAmbiguousFields(t *testing.T) {
	fields := []string{
		"description: First\ndescription: >-\n  Second",
		"description: |-\n  First\nDescription: Second",
		"description: Valid\nmetadata:\n  author: First\nmetadata:\n  author: Second",
		"description: Valid\nAllowed-Tools: read",
		"description: Valid\nallowed-tools: read\n\"allowed-tools\": other",
		"description: Valid\n'allowed-tools': other\nallowed-tools: read",
		"description: Valid\n\"description\": Other text",
		"'description': Other text\ndescription: Valid",
		"description: Valid\n\"Allowed-Tools\": other",
		"description: Valid\nmetadata:\n  author: First\n\"metadata\":\n  author: Second",
		"description: Valid\nlicense: MIT\n'license':",
		"description: Valid\ncompatibility: Any runtime\n\"compatibility\": Other runtime",
		"description: Valid\n\"name\": archived-skill",
	}
	for i, fields := range fields {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%d/%q", i, newline), func(t *testing.T) {
				server := newTestServer(t)
				setIndex(t, server, archiveEntry("archived-skill"))
				body := "---\nname: archived-skill\n" + fields + "\n---\nBody."
				server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(body, "\n", newline)}), "application/zip")
				source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})
				if got := loadSkills(t, source); len(got) != 0 {
					t.Fatalf("ambiguous frontmatter returned %d skills", len(got))
				}
			})
		}
	}
}

func TestSource_ArchiveFrontmatter_PreservesIndentedValues(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("%q", newline), func(t *testing.T) {
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			body := "---\nname: archived-skill\ndescription:\n  'Read files'\nlicense: MIT\n---\nBody."
			server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(body, "\n", newline)}), "application/zip")
			skill := loadOne(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)}))
			if skill.Frontmatter.Description != "Read files" || skill.Frontmatter.License != "MIT" {
				t.Fatalf("Frontmatter = %#v", skill.Frontmatter)
			}
		})
	}
}

func TestSource_ArchiveFrontmatter_EmptyOptionalsRemainZeroValues(t *testing.T) {
	for _, field := range []string{"license", "compatibility", "allowed-tools"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%s/%q", field, newline), func(t *testing.T) {
				server := newTestServer(t)
				setIndex(t, server, archiveEntry("archived-skill"))
				body := "---\nname: archived-skill\ndescription: Read files\n" + field + ": \t\n---\nBody."
				server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(body, "\n", newline)}), "application/zip")
				skill := loadOne(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)}))
				fm := skill.Frontmatter
				if fm.Description != "Read files" || fm.License != "" || fm.Compatibility != "" || fm.AllowedTools != "" {
					t.Fatalf("Frontmatter = %#v", fm)
				}
			})
		}
	}
}

func TestSource_ArchiveFrontmatter_DuplicateMetadataKeepsFirstAndLogs(t *testing.T) {
	for _, secondKey := range []string{"author", "Author"} {
		for _, quote := range []string{"", "'", `"`} {
			for _, newline := range []string{"\n", "\r\n"} {
				t.Run(fmt.Sprintf("%s/%q/%q", secondKey, quote, newline), func(t *testing.T) {
					server := newTestServer(t)
					setIndex(t, server, archiveEntry("archived-skill"))
					body := fmt.Sprintf("---\n%[1]sname%[1]s: archived-skill\n%[1]sdescription%[1]s: >-\n  Read\n  files\n"+
						"%[1]scompatibility%[1]s:\n  Any runtime\n%[1]sallowed-tools%[1]s: 'read'\n%[1]smetadata%[1]s:\n"+
						"  author:\n    'First'\n  %[2]s: Second\n  author: Third\n  version: '1.0'\n"+
						"%[1]slicense%[1]s: |-\n  MIT\n  License\n---\nBody.", quote, secondKey)
					server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(body, "\n", newline)}), "application/zip")
					var logs bytes.Buffer
					source := newSource(t, server, mcpskills.Config{
						ArchiveDirectory: extractionRoot(t),
						Logger:           slog.New(slog.NewTextHandler(&logs, nil)),
					})
					skill := loadOne(t, source)
					fm := skill.Frontmatter
					if len(fm.Metadata) != 2 || fm.Metadata["author"] != "First" || fm.Metadata["version"] != "1.0" {
						t.Fatalf("Metadata = %#v", fm.Metadata)
					}
					if fm.Description != "Read files" || fm.License != "MIT\nLicense" || fm.Compatibility != "Any runtime" || fm.AllowedTools != "read" {
						t.Fatalf("Frontmatter = %#v", fm)
					}
					if !strings.Contains(content(t, skill), "Body.") {
						t.Fatal("frontmatter processing lost the skill body")
					}
					if got := logs.String(); strings.Count(got, "duplicate metadata key") != 2 || strings.Count(got, "keeping the first value") != 2 || strings.Count(got, "level=WARN") != 2 {
						t.Fatalf("duplicate metadata warnings = %q", got)
					}
				})
			}
		}
	}
}

func TestSource_ArchiveFrontmatter_PreservesMetadataScalarFormats(t *testing.T) {
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "First", want: "First"},
		{value: "\n    'First'", want: "First"},
		{value: "|-\n    First\n    Second", want: "|-"},
		{value: ">-\n    First\n    Second", want: ">-"},
	} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%q/%q", test.value, newline), func(t *testing.T) {
				server := newTestServer(t)
				setIndex(t, server, archiveEntry("archived-skill"))
				body := "---\nname: archived-skill\ndescription: Read files\nmetadata:\n  author: " + test.value + "\n  version: '1.0'\n---\nBody."
				server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(body, "\n", newline)}), "application/zip")
				skill := loadOne(t, newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)}))
				if got := skill.Frontmatter.Metadata; len(got) != 2 || got["author"] != test.want || got["version"] != "1.0" {
					t.Fatalf("Metadata = %#v, want author %q", got, test.want)
				}
				if !strings.Contains(content(t, skill), "Body.") {
					t.Fatal("metadata parsing lost the skill body")
				}
			})
		}
	}
}

func TestSource_Close_RemovesOnlyOwnedTemporaryDirectory(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	temporaryRoot := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, temporaryRoot)
	}
	source := newSource(t, server, mcpskills.Config{})
	_ = loadOne(t, source)
	entries, err := os.ReadDir(temporaryRoot)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "agent-framework-mcp-skills-") {
		t.Fatalf("temporary directories = %#v, %v", entries, err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, filepath.Join(temporaryRoot, entries[0].Name()))

	explicit := extractionRoot(t)
	configured := newSource(t, server, mcpskills.Config{ArchiveDirectory: explicit})
	_ = loadOne(t, configured)
	if err := configured.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(explicit, "archived-skill", "SKILL.md")); err != nil {
		t.Fatalf("Close() removed a caller-configured archive: %v", err)
	}
}

func TestSource_ArchiveAmbiguousFrontmatter_IsSkipped(t *testing.T) {
	for i, test := range []struct {
		fields  string
		newline string
	}{
		{fields: "description: First\ndescription: >-\n  Second", newline: "\n"},
		{fields: "description: |-\n  First\nDescription: Second", newline: "\n"},
		{fields: "description: Valid\nmetadata:\n  author: First\nmetadata:\n  author: Second", newline: "\n"},
		{fields: "description: Valid\nAllowed-Tools: read", newline: "\n"},
		{fields: "description: Valid\nallowed-tools: read\n\"allowed-tools\": other", newline: "\n"},
		{fields: "description: Valid\nallowed-tools: read\n\"allowed-tools\": other", newline: "\r\n"},
		{fields: "description: Valid\n'allowed-tools': other\nallowed-tools: read", newline: "\n"},
		{fields: "description: Valid\n'allowed-tools': other\nallowed-tools: read", newline: "\r\n"},
		{fields: "description: Valid\n\"description\": Other text", newline: "\n"},
		{fields: "'description': Other text\ndescription: Valid", newline: "\r\n"},
		{fields: "description: Valid\n\"Allowed-Tools\": other", newline: "\n"},
		{fields: "description: Valid\nmetadata:\n  author: First\n\"metadata\":\n  author: Second", newline: "\r\n"},
		{fields: "description: Valid\nlicense: MIT\n'license':", newline: "\n"},
		{fields: "description: Valid\ncompatibility: Any runtime\n\"compatibility\": Other runtime", newline: "\r\n"},
		{fields: "description: Valid\n\"name\": archived-skill", newline: "\n"},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			body := "---\nname: archived-skill\n" + test.fields + "\n---\nBody."
			server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(body, "\n", test.newline)}), "application/zip")
			source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

			if got := loadSkills(t, source); len(got) != 0 {
				t.Fatalf("ambiguous frontmatter returned %d skills", len(got))
			}
		})
	}
}

func TestSource_ZIPArchive_ReturnsMetadataAndContent(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	skill := loadOne(t, source)
	if skill.Frontmatter.Name != "archived-skill" || skill.Frontmatter.Description != "A skill delivered as an archive." {
		t.Fatalf("Frontmatter = %#v", skill.Frontmatter)
	}
	if got := content(t, skill); !strings.Contains(got, "Body from the archive.") {
		t.Fatalf("GetContent() = %q", got)
	}
}

func TestSource_TarGzArchive_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	entry := archiveEntry("archived-skill")
	entry["url"] = "skill://archives/archived-skill.tar.gz"
	setIndex(t, server, entry)
	server.blob("skill://archives/archived-skill.tar.gz", []byte{0x1f, 0x8b}, "application/gzip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("TAR.GZ archive returned %d skills", len(got))
	}
}

func TestSource_SeparateArchiveDirectories_DoNotCollide(t *testing.T) {
	serverA := newTestServer(t)
	setIndex(t, serverA, archiveEntry("shared-skill"))
	serverA.blob("skill://archives/shared-skill.zip", makeZIP(t, archiveFile{"SKILL.md", fmt.Sprintf(sharedSkillMD, "A")}), "application/zip")
	serverB := newTestServer(t)
	setIndex(t, serverB, archiveEntry("shared-skill"))
	serverB.blob("skill://archives/shared-skill.zip", makeZIP(t, archiveFile{"SKILL.md", fmt.Sprintf(sharedSkillMD, "B")}), "application/zip")
	root := extractionRoot(t)
	sourceA := newSource(t, serverA, mcpskills.Config{ArchiveDirectory: filepath.Join(root, "a")})
	sourceB := newSource(t, serverB, mcpskills.Config{ArchiveDirectory: filepath.Join(root, "b")})

	skillA, skillB := loadOne(t, sourceA), loadOne(t, sourceB)
	if skillA.Frontmatter.Name != "shared-skill" || skillB.Frontmatter.Name != "shared-skill" {
		t.Fatalf("skill names = %q, %q", skillA.Frontmatter.Name, skillB.Frontmatter.Name)
	}
	if !strings.Contains(content(t, skillA), "Content from server A.") || !strings.Contains(content(t, skillB), "Content from server B.") {
		t.Fatal("separate directories did not preserve each server's content")
	}
}

func TestSource_FirstRun_PrunesLeftoverArchive(t *testing.T) {
	root := extractionRoot(t)
	stale := writeStaleArchiveSkill(t, root)
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})

	skills := loadSkills(t, source)
	assertMissing(t, stale)
	if len(skills) != 1 || skills[0].Frontmatter.Name != "archived-skill" {
		t.Fatalf("Skills() = %#v, want only archived-skill", skills)
	}
}

func TestSource_NoLongerAdvertisedArchive_IsPrunedOnFirstLoad(t *testing.T) {
	root := extractionRoot(t)
	fullServer := newTestServer(t)
	first, second := archiveEntry("skill-a"), archiveEntry("skill-b")
	first["description"], second["description"] = "Skill A.", "Skill B."
	setIndex(t, fullServer, first, second)
	fullServer.blob("skill://archives/skill-a.zip", makeZIP(t, archiveFile{"SKILL.md", skillAMD}), "application/zip")
	fullServer.blob("skill://archives/skill-b.zip", makeZIP(t, archiveFile{"SKILL.md", skillBMD}), "application/zip")
	firstSource := newSource(t, fullServer, mcpskills.Config{ArchiveDirectory: root})
	if got := loadSkills(t, firstSource); len(got) != 2 {
		t.Fatalf("initial discovery returned %d skills", len(got))
	}
	if _, err := os.Stat(filepath.Join(root, "skill-b")); err != nil {
		t.Fatal(err)
	}
	if err := firstSource.Close(); err != nil {
		t.Fatal(err)
	}
	partialServer := newTestServer(t)
	setIndex(t, partialServer, archiveEntry("skill-a"))
	partialServer.blob("skill://archives/skill-a.zip", makeZIP(t, archiveFile{"SKILL.md", skillAMD}), "application/zip")
	secondSource := newSource(t, partialServer, mcpskills.Config{ArchiveDirectory: root})

	skill := loadOne(t, secondSource)
	if skill.Frontmatter.Name != "skill-a" {
		t.Fatalf("remaining skill = %q", skill.Frontmatter.Name)
	}
	assertMissing(t, filepath.Join(root, "skill-b"))
	if _, err := os.Stat(filepath.Join(root, "skill-a")); err != nil {
		t.Fatal(err)
	}
}

func TestSource_NoArchiveEntries_PrunesLeftovers(t *testing.T) {
	root := extractionRoot(t)
	stale := writeStaleArchiveSkill(t, root)
	server := newTestServer(t)
	server.text(indexURI, `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[]}`)
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("empty index returned %d skills", len(got))
	}
	assertMissing(t, stale)
}

func TestSource_SecondDiscovery_ReExtractsArchive(t *testing.T) {
	root := extractionRoot(t)
	serverA := newTestServer(t)
	setIndex(t, serverA, archiveEntry("shared-skill"))
	serverA.blob("skill://archives/shared-skill.zip", makeZIP(t, archiveFile{"SKILL.md", fmt.Sprintf(sharedSkillMD, "A")}), "application/zip")
	first := newSource(t, serverA, mcpskills.Config{ArchiveDirectory: root})
	_ = loadOne(t, first)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serverA.session.Close(); err != nil {
		t.Fatal(err)
	}
	serverB := newTestServer(t)
	setIndex(t, serverB, archiveEntry("shared-skill"))
	serverB.blob("skill://archives/shared-skill.zip", makeZIP(t, archiveFile{"SKILL.md", fmt.Sprintf(sharedSkillMD, "B")}), "application/zip")
	second := newSource(t, serverB, mcpskills.Config{ArchiveDirectory: root})

	if got := content(t, loadOne(t, second)); !strings.Contains(got, "Content from server B.") {
		t.Fatalf("second discovery returned stale content: %q", got)
	}
}

func TestSource_ArchiveExceedsDefaultFileCount_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, manyFileZIP(t), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("archive exceeding the default file count returned %d skills", len(got))
	}
}

func TestSource_ArchiveExceedsConfiguredSize_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
	source := newSource(t, server, mcpskills.Config{
		ArchiveDirectory:    extractionRoot(t),
		ArchiveMaxSizeBytes: new(int64(1)),
	})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("archive exceeding the configured download size returned %d skills", len(got))
	}
}

func TestSource_RaisedFileCount_LoadsLargerArchive(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("archived-skill"))
	server.blob(archiveURI, manyFileZIP(t), "application/zip")
	source := newSource(t, server, mcpskills.Config{
		ArchiveDirectory:    extractionRoot(t),
		ArchiveMaxFileCount: new(manyFileArchiveFileCount + 1),
	})

	if skill := loadOne(t, source); skill.Frontmatter.Name != "archived-skill" {
		t.Fatalf("loaded skill = %q", skill.Frontmatter.Name)
	}
}

func TestSource_ArchiveMissingName_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[
{"name":"","type":"archive","description":"No name.","url":"skill://archives/x.zip"}]}`)
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("missing archive name returned %d skills", len(got))
	}
}

func TestSource_ArchiveInvalidName_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[
{"name":"../escape","type":"archive","description":"Bad name.","url":"skill://archives/x.zip"}]}`)
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("invalid archive name returned %d skills", len(got))
	}
}

func TestSource_ArchiveMissingURL_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[
{"name":"good-name","type":"archive","description":"No url.","url":""}]}`)
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("missing archive URL returned %d skills", len(got))
	}
}

func TestSource_ArchiveUnsupportedFormat_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	entry := archiveEntry("bad-format")
	entry["url"] = "skill://archives/bad-format.bin"
	setIndex(t, server, entry)
	server.blob("skill://archives/bad-format.bin", []byte("not an archive"), "application/octet-stream")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("unsupported archive format returned %d skills", len(got))
	}
}

func TestSource_ArchiveTextInsteadOfBlob_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	setIndex(t, server, archiveEntry("text-skill"))
	server.text("skill://archives/text-skill.zip", "this is not binary")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("text archive response returned %d skills", len(got))
	}
}

func TestSource_ConcurrentArchiveCalls_ReconcileSafely(t *testing.T) {
	server := newTestServer(t)
	first, second := archiveEntry("skill-a"), archiveEntry("skill-b")
	first["description"], second["description"] = "Skill A.", "Skill B."
	setIndex(t, server, first, second)
	server.blob("skill://archives/skill-a.zip", makeZIP(t, archiveFile{"SKILL.md", skillAMD}), "application/zip")
	server.blob("skill://archives/skill-b.zip", makeZIP(t, archiveFile{"SKILL.md", skillBMD}), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})
	type result struct {
		skills []*skills.Skill
		err    error
	}
	done := make(chan result, 20)
	for range 20 {
		go func() {
			skills, err := source.Skills(t.Context(), skills.SourceContext{})
			done <- result{skills, err}
		}()
	}

	for range 20 {
		got := <-done
		if got.err != nil || len(got.skills) != 2 {
			t.Fatalf("Skills() = %#v, %v", got.skills, got.err)
		}
		byName := make(map[string]*skills.Skill, len(got.skills))
		for _, skill := range got.skills {
			byName[skill.Frontmatter.Name] = skill
		}
		for name, want := range map[string]string{"skill-a": "Content A.", "skill-b": "Content B."} {
			skill := byName[name]
			if skill == nil {
				t.Fatalf("missing skill %q", name)
			}
			if body := content(t, skill); !strings.Contains(body, want) {
				t.Fatalf("GetContent(%q) = %q, want %q", name, body, want)
			}
		}
	}
}

func TestSource_MatchingArchiveDigest_SkipsTraversalEntry(t *testing.T) {
	server := newTestServer(t)
	archive := makeZIP(t, archiveFile{"SKILL.md", archivedMD}, archiveFile{"../escape.md", "Outside skill."})
	entry := archiveEntry("archived-skill")
	entry["digest"] = digest(archive)
	setIndex(t, server, entry)
	server.blob(archiveURI, archive, "application/zip")
	root := extractionRoot(t)
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})

	_ = loadOne(t, source)
	if _, err := os.Stat(filepath.Join(root, "archived-skill", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, filepath.Join(root, "escape.md"))
}

func TestSource_RejectedArchiveDigest_KeepsValidSibling(t *testing.T) {
	for _, rejectedDigest := range []string{"", "sha256:" + strings.Repeat("0", 64)} {
		t.Run(rejectedDigest, func(t *testing.T) {
			server := newTestServer(t)
			rejectedArchive := makeZIP(t, archiveFile{"SKILL.md", archivedMD})
			validArchive := makeZIP(t, archiveFile{"SKILL.md", skillAMD})
			rejected, valid := archiveEntry("archived-skill"), archiveEntry("skill-a")
			rejected["digest"] = rejectedDigest
			valid["description"], valid["digest"] = "Skill A.", digest(validArchive)
			setIndex(t, server, rejected, valid)
			server.blob(archiveURI, rejectedArchive, "application/zip")
			server.blob("skill://archives/skill-a.zip", validArchive, "application/zip")
			root := extractionRoot(t)
			source := newSource(t, server, mcpskills.Config{ArchiveDirectory: root})

			if skill := loadOne(t, source); skill.Frontmatter.Name != "skill-a" {
				t.Fatalf("remaining skill = %q", skill.Frontmatter.Name)
			}
			assertMissing(t, filepath.Join(root, "archived-skill"))
			if _, err := os.Stat(filepath.Join(root, "skill-a", "SKILL.md")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSource_ArchiveTargetRegularFile_IsPreserved(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(fmt.Sprint("archive available=", available), func(t *testing.T) {
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("archived-skill"))
			if available {
				server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", archivedMD}), "application/zip")
			}
			root := t.TempDir()
			target := filepath.Join(root, "archived-skill")
			const marker = "caller-owned root file"
			if err := os.WriteFile(target, []byte(marker), 0o600); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			source := newSource(t, server, mcpskills.Config{
				ArchiveDirectory: root,
				Logger:           slog.New(slog.NewTextHandler(&logs, nil)),
			})

			if got := loadSkills(t, source); len(got) != 0 {
				t.Fatalf("regular-file collision returned %d skills", len(got))
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != marker {
				t.Fatalf("colliding root file = %q, %v, want preserved contents", data, err)
			}
			if got := logs.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "not a directory") {
				t.Fatalf("collision was not diagnosed: %q", got)
			}
		})
	}
}

func TestSource_ArchiveRootNameMismatch_IsSkipped(t *testing.T) {
	for _, sibling := range []bool{false, true} {
		t.Run(fmt.Sprint("valid sibling=", sibling), func(t *testing.T) {
			server := newTestServer(t)
			entries := []map[string]any{archiveEntry("archived-skill")}
			server.blob(archiveURI, makeZIP(t, archiveFile{"SKILL.md", strings.ReplaceAll(archivedMD, "archived-skill", "unexpected-skill")}), "application/zip")
			if sibling {
				entries = append(entries, archiveEntry("skill-a"))
				server.blob("skill://archives/skill-a.zip", makeZIP(t, archiveFile{"SKILL.md", skillAMD}), "application/zip")
			}
			setIndex(t, server, entries...)
			var logs bytes.Buffer
			source := newSource(t, server, mcpskills.Config{
				ArchiveDirectory: extractionRoot(t),
				Logger:           slog.New(slog.NewTextHandler(&logs, nil)),
			})

			got := loadSkills(t, source)
			if sibling {
				if len(got) != 1 || got[0].Frontmatter.Name != "skill-a" {
					t.Fatalf("Skills() = %#v, want only the valid sibling", got)
				}
			} else if len(got) != 0 {
				t.Fatalf("mismatched root metadata returned %d skills", len(got))
			}
			if text := logs.String(); !strings.Contains(text, "level=ERROR") ||
				!strings.Contains(text, "unexpected-skill") || !strings.Contains(text, "archived-skill") {
				t.Fatalf("name mismatch was not diagnosed: %q", text)
			}
		})
	}
}

func TestSource_ArchiveBundles_PreserveNestedSkillDiscovery(t *testing.T) {
	for _, directory := range []string{"nested-skill", "folder/nested-skill"} {
		t.Run(directory, func(t *testing.T) {
			server := newTestServer(t)
			setIndex(t, server, archiveEntry("bundle"))
			server.blob("skill://archives/bundle.zip", makeZIP(t,
				archiveFile{directory + "/SKILL.md", strings.ReplaceAll(archivedMD, "archived-skill", "nested-skill")},
				archiveFile{directory + "/reference.md", "Nested resource."},
			), "application/zip")
			source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

			skill := loadOne(t, source)
			if skill.Frontmatter.Name != "nested-skill" {
				t.Fatalf("nested skill name = %q", skill.Frontmatter.Name)
			}
			if got := readStaticResource(t, skill, "reference.md"); got != "Nested resource." {
				t.Fatalf("nested resource = %#v", got)
			}
		})
	}
}

func TestSource_DuplicateArchiveEntries_DiscoverFinalExtraction(t *testing.T) {
	server := newTestServer(t)
	first, second := archiveEntry("archived-skill"), archiveEntry("archived-skill")
	first["url"], second["url"] = "skill://archives/first.zip", "skill://archives/second.zip"
	setIndex(t, server, first, second)
	server.blob("skill://archives/first.zip", makeZIP(t, archiveFile{"SKILL.md", archivedMD + "\nFirst version."}), "application/zip")
	server.blob("skill://archives/second.zip", makeZIP(t, archiveFile{"SKILL.md", archivedMD + "\nSecond version."}), "application/zip")
	source := newSource(t, server, mcpskills.Config{ArchiveDirectory: extractionRoot(t)})

	got := loadSkills(t, source)
	if len(got) != 2 {
		t.Fatalf("Skills() returned %d skills, want both advertised entries", len(got))
	}
	for _, skill := range got {
		if text := content(t, skill); !strings.Contains(text, "Second version.") || strings.Contains(text, "First version.") {
			t.Fatalf("discovery observed an intermediate extraction: %q", text)
		}
	}
}

func TestSource_FailedDuplicateArchive_DoesNotAbortDiscovery(t *testing.T) {
	for _, failure := range []string{"missing resource", "rejected digest", "invalid ZIP"} {
		for _, sibling := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/valid sibling=%t", failure, sibling), func(t *testing.T) {
				server := newTestServer(t)
				first, second := archiveEntry("archived-skill"), archiveEntry("archived-skill")
				first["url"], second["url"] = "skill://archives/first.zip", "skill://archives/second.zip"
				archive := makeZIP(t, archiveFile{"SKILL.md", archivedMD})
				server.blob("skill://archives/first.zip", archive, "application/zip")
				switch failure {
				case "rejected digest":
					second["digest"] = "sha256:" + strings.Repeat("0", 64)
					server.blob("skill://archives/second.zip", archive, "application/zip")
				case "invalid ZIP":
					server.blob("skill://archives/second.zip", []byte{'P', 'K', 3, 4}, "application/zip")
				}
				entries := []map[string]any{first, second}
				if sibling {
					entries = append(entries, archiveEntry("skill-a"))
					server.blob("skill://archives/skill-a.zip", makeZIP(t, archiveFile{"SKILL.md", skillAMD}), "application/zip")
				}
				setIndex(t, server, entries...)
				root := extractionRoot(t)
				var logs bytes.Buffer
				source := newSource(t, server, mcpskills.Config{
					ArchiveDirectory: root,
					Logger:           slog.New(slog.NewTextHandler(&logs, nil)),
				})

				got := loadSkills(t, source)
				if sibling {
					if len(got) != 1 || got[0].Frontmatter.Name != "skill-a" {
						t.Fatalf("Skills() = %#v, want only the valid sibling", got)
					}
					if body := content(t, got[0]); !strings.Contains(body, "Content A.") {
						t.Fatalf("valid sibling content = %q", body)
					}
				} else if len(got) != 0 {
					t.Fatalf("failed duplicate returned %d skills", len(got))
				}
				assertMissing(t, filepath.Join(root, "archived-skill"))
				if text := logs.String(); !strings.Contains(text, "Skipping missing extracted archive skill directory") {
					t.Fatalf("missing extracted directory was not diagnosed: %q", text)
				}
			})
		}
	}
}
