// Copyright (c) Microsoft. All rights reserved.

package fsskills_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
)

type fsWithoutLinkInspection struct {
	fs.FS
}

func TestFileSource_EmptyPaths_ReturnsEmptyList(t *testing.T) {
	source := fsskills.NewSource()

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected no skills, got %d", len(loaded))
	}
}

func TestFileSource_NilFilesystemPanics(t *testing.T) {
	defer func() {
		if got := recover(); got != "fsskills: filesystem at index 1 is nil" {
			t.Fatalf("panic = %v, want indexed nil filesystem message", got)
		}
	}()

	fsskills.NewSource(os.DirFS(t.TempDir()), nil)
}

func TestFileSource_NonExistentPath_ReturnsEmptyList(t *testing.T) {
	root := t.TempDir()
	source := fsskills.NewSource(os.DirFS(filepath.Join(root, "does-not-exist")))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected no skills, got %d", len(loaded))
	}
}

func TestFileSource_NoResourceFiles_ReturnsEmptyResources(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "no-resources", "A skill", "No resources here.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if len(loaded[0].Resources) != 0 {
		t.Fatalf("expected no resources, got %d", len(loaded[0].Resources))
	}
}

func TestFileSkill_WithoutResources_ContentIncludesEmptyAvailableResourcesBlock(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "no-resource-content", "A skill", "No resources here.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	content, err := loaded[0].GetContent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "<available_resources />") {
		t.Fatalf("expected empty <available_resources /> block when skill has no resources, got: %s", content)
	}
}

func TestFileSkill_WithResources_ContentIncludesAvailableResourcesBlock(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "resource-content", "A skill", "Use these resources.", "references/doc.md", "Document content.")
	createRelativeFile(t, filepath.Join(root, "resource-content"), "assets/config.json", "{}")
	const originalRawContent = "---\nname: resource-content\ndescription: A skill\n---\nUse these resources."
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	content, err := loaded[0].GetContent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(content, originalRawContent) {
		t.Fatalf("expected content to start with original SKILL.md content %q, got: %s", originalRawContent, content)
	}
	if !strings.Contains(content, "<available_resources>") {
		t.Fatalf("expected <available_resources> block in content, got: %s", content)
	}
	if !strings.Contains(content, `<resource name="assets/config.json"/>`) {
		t.Fatalf("expected assets/config.json resource in content, got: %s", content)
	}
	if !strings.Contains(content, `<resource name="references/doc.md"/>`) {
		t.Fatalf("expected references/doc.md resource in content, got: %s", content)
	}
	if !strings.Contains(content, "</available_resources>") {
		t.Fatalf("expected </available_resources> in content, got: %s", content)
	}
	if !strings.Contains(content, "<available_scripts />") {
		t.Fatalf("expected empty <available_scripts /> block when skill has no scripts, got: %s", content)
	}
}

func TestFileSource_NestedSkillDirectory_DiscoveredWithinDepthLimit(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, filepath.Join(root, "level1"), "nested-skill", "Nested", "Nested body.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Name != "nested-skill" {
		t.Fatalf("expected nested-skill, got %q", loaded[0].Frontmatter.Name)
	}
}

func TestFileSource_RootSkillFileWithNestedSkillFile_DoesNotAbortDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: root-skill\ndescription: Root\n---\nRoot body."), 0o644); err != nil {
		t.Fatal(err)
	}
	childDir := filepath.Join(root, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "SKILL.md"), []byte("---\nname: child\ndescription: Child\n---\nChild body."), 0o644); err != nil {
		t.Fatal(err)
	}

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Name != "root-skill" {
		t.Fatalf("expected root-skill, got %q", loaded[0].Frontmatter.Name)
	}
}

func TestFileSource_SymlinkedSkillFile_IsSkipped(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "linked-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideSkillFile := filepath.Join(t.TempDir(), "outside-SKILL.md")
	if err := os.WriteFile(outsideSkillFile, []byte("---\nname: linked-skill\ndescription: Linked skill file\n---\nBody."), 0o644); err != nil {
		t.Fatal(err)
	}
	createSymlink(t, filepath.Join(skillDir, "SKILL.md"), outsideSkillFile)

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected symlinked SKILL.md to be skipped, got %d skills", len(loaded))
	}
}

func TestFileSource_SymlinkedSkillFile_DoesNotAbortNestedDiscovery(t *testing.T) {
	root := t.TempDir()
	linkedSkillDir := filepath.Join(root, "linked-skill")
	if err := os.MkdirAll(linkedSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideSkillFile := filepath.Join(root, "outside-SKILL.md")
	if err := os.WriteFile(outsideSkillFile, []byte("---\nname: linked-skill\ndescription: Linked skill file\n---\nBody."), 0o644); err != nil {
		t.Fatal(err)
	}
	createSymlink(t, filepath.Join(linkedSkillDir, "SKILL.md"), outsideSkillFile)

	nestedSkillDir := filepath.Join(linkedSkillDir, "nested-skill")
	createSkillDir(t, filepath.Dir(nestedSkillDir), "nested-skill", "Nested", "Nested body.")

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Name != "nested-skill" {
		t.Fatalf("expected nested-skill, got %q", loaded[0].Frontmatter.Name)
	}
}

func TestFileSource_ConfiguredRootSymlink_StillDiscoversSkills(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real-root")
	linkedRoot := filepath.Join(root, "linked-root")
	createSkillDir(t, realRoot, "my-skill", "A skill", "Body.")
	createSymlink(t, linkedRoot, realRoot)

	source := fsskills.NewSource(os.DirFS(linkedRoot))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Name != "my-skill" {
		t.Fatalf("expected my-skill, got %q", loaded[0].Frontmatter.Name)
	}
}

func TestFileSource_NestedSkillFileUnderSkillRoot_NotDiscoveredAsIndependentSkill(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "parent-skill", "Parent", "Parent body.")
	childDir := filepath.Join(root, "parent-skill", "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "SKILL.md"), []byte("---\nname: child\ndescription: Child\n---\nChild body."), 0o644); err != nil {
		t.Fatal(err)
	}

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Name != "parent-skill" {
		t.Fatalf("expected parent-skill, got %q", loaded[0].Frontmatter.Name)
	}
}

func TestFileSource_SkillBeyondMaxDepth_NotDiscovered(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, filepath.Join(root, "l1", "l2", "l3"), "deep-skill", "Too deep", "Body.")
	createSkillDir(t, filepath.Join(root, "l1"), "within-depth-skill", "Within depth", "Body.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range loaded {
		if skill.Frontmatter.Name == "deep-skill" {
			t.Fatal("expected deep-skill not to be discovered beyond max search depth")
		}
	}
	if len(loaded) != 1 {
		t.Fatalf("expected only the reachable skill, got %d skills", len(loaded))
	}
	if loaded[0].Frontmatter.Name != "within-depth-skill" {
		t.Fatalf("expected within-depth-skill, got %q", loaded[0].Frontmatter.Name)
	}
}

func TestFileSource_SearchDepth_DoesNotAffectSkillDirectoryDiscovery(t *testing.T) {
	root := t.TempDir()
	// A skill directory nested four levels below the filesystem root.
	createSkillDir(t, filepath.Join(root, "l1", "l2", "l3"), "deep-skill", "Deep", "Body.")

	// SearchDepth governs only within-skill resource/script discovery; it must
	// not widen skill-directory discovery, which is bounded independently. Even
	// a large SearchDepth leaves the deeply-nested skill directory undiscovered.
	deep := fsskills.NewSourceOptions(fsskills.SourceOptions{SearchDepth: new(4)}, os.DirFS(root))
	loaded, err := deep.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range loaded {
		if skill.Frontmatter.Name == "deep-skill" {
			t.Fatal("SearchDepth must not cause skill-directory discovery beyond the fixed bound")
		}
	}
}

func TestFileSource_ReadResource_ValidResource_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "read-skill", "A skill", "See docs.")
	want := map[string]string{
		"references/doc.md":   "Document content here.",
		"assets/doc.md":       "Other document content.",
		"references/empty.md": "",
	}
	for name, content := range want {
		createRelativeFile(t, filepath.Join(root, "read-skill"), name, content)
	}
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if len(loaded[0].Resources) != len(want) {
		t.Fatalf("expected %d resources, got %d", len(want), len(loaded[0].Resources))
	}
	for _, resource := range loaded[0].Resources {
		expected, ok := want[resource.Name]
		if !ok {
			t.Fatalf("unexpected or duplicate resource %q", resource.Name)
		}
		value, err := resource.Read(t.Context())
		if err != nil {
			t.Fatalf("Read(%q): %v", resource.Name, err)
		}
		content, ok := value.(string)
		if !ok {
			t.Fatalf("Read(%q) returned %T, want string", resource.Name, value)
		}
		if content != expected {
			t.Errorf("Read(%q) = %q, want %q", resource.Name, content, expected)
		}
		delete(want, resource.Name)
	}
}

func TestFileSource_ReadResource_RevalidatesParentDirectoriesBeforeUse(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, filepath.Join(root, "trusted"), "read-skill", "A skill", "See docs.", "references/doc.md", "trusted content")
	createSkillDirWithResource(t, filepath.Join(root, "outside", "trusted"), "read-skill", "A skill", "See docs.", "references/doc.md", "outside content")

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 {
		t.Fatalf("expected one skill with one resource, got %d skills and %d resources", len(loaded), len(loaded[0].Resources))
	}

	if err := os.Rename(filepath.Join(root, "trusted"), filepath.Join(root, "trusted-real")); err != nil {
		t.Fatal(err)
	}
	createSymlink(t, filepath.Join(root, "trusted"), filepath.Join(root, "outside", "trusted"))

	_, err = loaded[0].Resources[0].Read(t.Context())
	if err == nil {
		t.Fatal("expected resource read to fail after the discovered path was replaced with a symlink")
	}
}

func TestFileSource_ReadResource_FailsWithoutLinkInspection(t *testing.T) {
	source := fsskills.NewSource(fsWithoutLinkInspection{fstest.MapFS{
		"read-skill/SKILL.md":          {Data: []byte("---\nname: read-skill\ndescription: A skill\n---\nSee docs.")},
		"read-skill/references/doc.md": {Data: []byte("content")},
	}})

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 {
		t.Fatalf("expected one skill with one resource, got %d skills and %d resources", len(loaded), len(loaded[0].Resources))
	}

	if _, err := loaded[0].Resources[0].Read(t.Context()); err == nil {
		t.Fatal("expected resource read to fail when the filesystem does not support link inspection")
	}
}

func TestFileSource_MetadataWithQuotedValues_ParsedCorrectly(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "quoted-meta", strings.Join([]string{
		"---",
		"name: quoted-meta",
		"description: Metadata with quotes",
		"metadata:",
		"  key1: 'single quoted'",
		"  key2: \"double quoted\"",
		"---",
		"Body.",
	}, "\n"))
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	fm := loaded[0].Frontmatter
	if fm.Metadata["key1"] != "single quoted" {
		t.Fatalf("expected single quoted metadata, got %#v", fm.Metadata["key1"])
	}
	if fm.Metadata["key2"] != "double quoted" {
		t.Fatalf("expected double quoted metadata, got %#v", fm.Metadata["key2"])
	}
}

func TestFileSource_BlockScalarDescription_ParsesMultilineValue(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "block-scalar-skill", strings.Join([]string{
		"---",
		"name: block-scalar-skill",
		"description: |",
		"  This is a multiline",
		"  description for the skill.",
		"---",
		"Body text.",
	}, "\n"))
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Description != "This is a multiline\ndescription for the skill." {
		t.Fatalf("unexpected description: %q", loaded[0].Frontmatter.Description)
	}
}

func TestFileSource_FoldedScalarDescription_ParsesMultilineValue(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "folded-scalar-skill", strings.Join([]string{
		"---",
		"name: folded-scalar-skill",
		"description: >",
		"  This is a multiline",
		"  description for the skill.",
		"---",
		"Body text.",
	}, "\n"))
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Description != "This is a multiline description for the skill." {
		t.Fatalf("unexpected description: %q", loaded[0].Frontmatter.Description)
	}
}

func TestFileSource_FoldedScalarDescription_PreservesParagraphBreaks(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "folded-paragraph-skill", strings.Join([]string{
		"---",
		"name: folded-paragraph-skill",
		"description: >",
		"  First paragraph line one",
		"  line two",
		"",
		"  Second paragraph.",
		"---",
		"Body text.",
	}, "\n"))
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Frontmatter.Description != "First paragraph line one line two\nSecond paragraph." {
		t.Fatalf("unexpected description: %q", loaded[0].Frontmatter.Description)
	}
}

func TestFileSource_ScalarDescriptionWithChompingIndicator_ParsesValue(t *testing.T) {
	tests := []struct {
		indicator string
		expected  string
	}{
		{indicator: "|-", expected: "This is a multiline\ndescription for the skill."},
		{indicator: "|+", expected: "This is a multiline\ndescription for the skill.\n"},
		{indicator: ">-", expected: "This is a multiline description for the skill."},
		{indicator: ">+", expected: "This is a multiline description for the skill.\n"},
	}

	for _, tt := range tests {
		t.Run(tt.indicator, func(t *testing.T) {
			root := t.TempDir()
			chomping := "strip"
			if tt.indicator[1] == '+' {
				chomping = "keep"
			}
			skillName := "chomping-scalar-skill-"
			if tt.indicator[0] == '|' {
				skillName += "literal-"
			} else {
				skillName += "folded-"
			}
			skillName += chomping
			createSkillDirRaw(t, root, skillName, strings.Join([]string{
				"---",
				"name: " + skillName,
				"description: " + tt.indicator,
				"  This is a multiline",
				"  description for the skill.",
				"---",
				"Body text.",
			}, "\n"))
			source := fsskills.NewSource(os.DirFS(root))

			loaded, err := source.Skills(t.Context(), skills.SourceContext{})
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 1 {
				t.Fatalf("expected 1 skill, got %d", len(loaded))
			}
			if loaded[0].Frontmatter.Description != tt.expected {
				t.Fatalf("unexpected description: %q", loaded[0].Frontmatter.Description)
			}
		})
	}
}

func TestFileSource_ParsesOptionalFrontmatterFields(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "meta-skill", strings.Join([]string{
		"---",
		"name: meta-skill",
		"description: A skill with metadata",
		"license: MIT",
		"compatibility: Requires Python 3.11+",
		"allowed-tools: grep glob view",
		"metadata:",
		"  author: contoso",
		"  tier: premium",
		"---",
		"Body.",
	}, "\n"))

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	fm := loaded[0].Frontmatter
	if fm.Name != "meta-skill" {
		t.Fatalf("expected name meta-skill, got %q", fm.Name)
	}
	if fm.Description != "A skill with metadata" {
		t.Fatalf("expected description A skill with metadata, got %q", fm.Description)
	}
	if fm.License != "MIT" {
		t.Fatalf("expected license MIT, got %q", fm.License)
	}
	if fm.Compatibility != "Requires Python 3.11+" {
		t.Fatalf("expected compatibility to be parsed, got %q", fm.Compatibility)
	}
	if fm.AllowedTools != "grep glob view" {
		t.Fatalf("expected allowed-tools to be parsed, got %q", fm.AllowedTools)
	}
	if fm.Metadata["author"] != "contoso" {
		t.Fatalf("expected metadata author contoso, got %#v", fm.Metadata["author"])
	}
	if fm.Metadata["tier"] != "premium" {
		t.Fatalf("expected metadata tier premium, got %#v", fm.Metadata["tier"])
	}
}

func TestFileSource_NoOptionalFields_DefaultZeroValues(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "basic-skill", "A basic skill", "Body.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	fm := loaded[0].Frontmatter
	if fm.Name != "basic-skill" || fm.Description != "A basic skill" {
		t.Fatalf("unexpected required frontmatter: %#v", fm)
	}
	if fm.License != "" || fm.Compatibility != "" || fm.AllowedTools != "" {
		t.Fatalf("expected zero-value optional fields, got %#v", fm)
	}
	if fm.Metadata != nil {
		t.Fatalf("expected nil metadata, got %#v", fm.Metadata)
	}
}

func TestFileSource_QuotedFrontmatterPropertyNames_AreParsed(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "quoted-root-keys", strings.Join([]string{
		"---",
		`"name": quoted-root-keys`,
		`'description': "A quoted root property skill"`,
		`"metadata":`,
		"  author: contoso",
		"---",
		"Body.",
	}, "\n"))
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	fm := loaded[0].Frontmatter
	if fm.Name != "quoted-root-keys" || fm.Description != "A quoted root property skill" {
		t.Fatalf("unexpected frontmatter: %#v", fm)
	}
	if fm.Metadata["author"] != "contoso" {
		t.Fatalf("expected metadata author contoso, got %#v", fm.Metadata["author"])
	}
}

func TestFileSource_AmbiguousFrontmatter_IsRejected(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
	}{
		{
			name: "duplicate recognized field",
			fields: []string{
				"description: first",
				"description: second",
			},
		},
		{
			name: "incorrectly cased recognized field",
			fields: []string{
				"Description: invalid casing",
			},
		},
		{
			name: "duplicate quoted recognized field",
			fields: []string{
				"allowed-tools: read",
				`"allowed-tools": write`,
			},
		},
		{
			name: "duplicate metadata root",
			fields: []string{
				"metadata:",
				"  author: first",
				`"metadata":`,
				"  author: second",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			lines := []string{"---", "name: ambiguous-skill"}
			lines = append(lines, tt.fields...)
			lines = append(lines, "---", "Body.")
			createSkillDirRaw(t, root, "ambiguous-skill", strings.Join(lines, "\n"))

			source := fsskills.NewSource(os.DirFS(root))
			loaded, err := source.Skills(t.Context(), skills.SourceContext{})
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 0 {
				t.Fatalf("expected ambiguous frontmatter to be rejected, got %d skill(s)", len(loaded))
			}
		})
	}
}

func TestFileSource_IndentedValueOnNextLine_IsParsed(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "indented-next-line", strings.Join([]string{
		"---",
		"name: indented-next-line",
		"description:",
		"  'Read files'",
		"license: MIT",
		"---",
		"Body.",
	}, "\n"))
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	fm := loaded[0].Frontmatter
	if fm.Description != "Read files" || fm.License != "MIT" {
		t.Fatalf("unexpected frontmatter: %#v", fm)
	}
}

func TestFileSource_IndentedBlockScalarOnNextLine_IsFolded(t *testing.T) {
	tests := []struct {
		name     string
		newline  string
		fields   []string
		expected string
	}{
		{
			name:    "folded scalar LF",
			newline: "\n",
			fields: []string{
				"description:",
				"  >-",
				"  Read",
				"  files",
			},
			expected: "Read files",
		},
		{
			name:    "literal scalar LF",
			newline: "\n",
			fields: []string{
				"description:",
				"  |-",
				"  Read",
				"  files",
			},
			expected: "Read\nfiles",
		},
		{
			name:    "folded scalar CRLF",
			newline: "\r\n",
			fields: []string{
				"description:",
				"  >-",
				"  Read",
				"  files",
			},
			expected: "Read files",
		},
		{
			name:    "literal scalar CRLF",
			newline: "\r\n",
			fields: []string{
				"description:",
				"  |-",
				"  Read",
				"  files",
			},
			expected: "Read\nfiles",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			skillName := "indented-block-scalar"
			lines := append([]string{"---", "name: " + skillName}, tt.fields...)
			lines = append(lines, "---", "Body.")
			createSkillDirRaw(t, root, skillName, strings.Join(lines, tt.newline))

			source := fsskills.NewSource(os.DirFS(root))
			loaded, err := source.Skills(t.Context(), skills.SourceContext{})
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 1 {
				t.Fatalf("expected 1 skill, got %d", len(loaded))
			}
			if loaded[0].Frontmatter.Description != tt.expected {
				t.Fatalf("unexpected description: %q", loaded[0].Frontmatter.Description)
			}
		})
	}
}

func TestFileSource_EmptyOptionalScalar_RemainsZeroValue(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "empty-optionals", strings.Join([]string{
		"---",
		"name: empty-optionals",
		"description: Read files",
		"license:   ",
		"compatibility:\t",
		"allowed-tools: ",
		"---",
		"Body.",
	}, "\n"))
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	fm := loaded[0].Frontmatter
	if fm.License != "" || fm.Compatibility != "" || fm.AllowedTools != "" {
		t.Fatalf("expected zero-value optional fields, got %#v", fm)
	}
}

func TestFileSource_DuplicateMetadata_KeepsFirstValue(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "duplicate-metadata", strings.Join([]string{
		"---",
		"name: duplicate-metadata",
		"description: Read files",
		"metadata:",
		"  author: First",
		"  Author: Second",
		"  author: Third",
		"  version: 1.0",
		"---",
		"Body.",
	}, "\n"))
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	fm := loaded[0].Frontmatter
	if len(fm.Metadata) != 2 {
		t.Fatalf("expected 2 metadata entries, got %#v", fm.Metadata)
	}
	if fm.Metadata["author"] != "First" {
		t.Fatalf("expected first metadata value to win, got %#v", fm.Metadata["author"])
	}
	if _, exists := fm.Metadata["Author"]; exists {
		t.Fatalf("expected first metadata key spelling to be preserved, got %#v", fm.Metadata)
	}
	if fm.Metadata["version"] != "1.0" {
		t.Fatalf("expected version metadata to be preserved, got %#v", fm.Metadata["version"])
	}
}

func TestFileSource_ResourcesInSubdirectory_DiscoveredWithDefaultDepth(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "sub-res-skill", "Subdirectory resources", "Body.")
	skillDir := filepath.Join(root, "sub-res-skill")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	resources := loaded[0].Resources
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].Name != "references/data.json" {
		t.Fatalf("expected references/data.json, got %q", resources[0].Name)
	}
}

func TestFileSource_ResourceFilter_IncludesOnlyMatchingFiles(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "filter-skill", "Filter test", "Body.")
	skillDir := filepath.Join(root, "filter-skill")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "keep.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "skip.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	createRelativeFile(t, skillDir, "docs/readme.md", "Docs.")

	for _, tt := range []struct {
		name   string
		filter func(fsskills.FilterContext) bool
		want   []string
	}{
		{
			name: "exact path",
			filter: func(ctx fsskills.FilterContext) bool {
				return ctx.RelativeFilePath == "references/keep.json"
			},
			want: []string{"references/keep.json"},
		},
		{
			name: "exclude directory",
			filter: func(ctx fsskills.FilterContext) bool {
				return !strings.HasPrefix(ctx.RelativeFilePath, "docs/")
			},
			want: []string{"references/keep.json", "references/skip.json"},
		},
		{
			name:   "include all",
			filter: func(fsskills.FilterContext) bool { return true },
			want:   []string{"docs/readme.md", "references/keep.json", "references/skip.json"},
		},
		{
			name:   "exclude all",
			filter: func(fsskills.FilterContext) bool { return false },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := fsskills.NewSourceOptions(fsskills.SourceOptions{
				ResourceFilter: func(ctx fsskills.FilterContext) bool {
					if ctx.SkillName != "filter-skill" {
						t.Fatalf("filter skill name = %q, want filter-skill", ctx.SkillName)
					}
					return tt.filter(ctx)
				},
			}, os.DirFS(root))
			loaded, err := source.Skills(t.Context(), skills.SourceContext{})
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 1 {
				t.Fatalf("expected 1 skill, got %d", len(loaded))
			}
			resourceNames := make([]string, 0, len(loaded[0].Resources))
			for _, resource := range loaded[0].Resources {
				resourceNames = append(resourceNames, resource.Name)
			}
			slices.Sort(resourceNames)
			if !slices.Equal(resourceNames, tt.want) {
				t.Fatalf("resource names = %v, want %v", resourceNames, tt.want)
			}
		})
	}
}

func TestFileSource_SearchDepth1_DoesNotDiscoverSubdirectoryResources(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "depth-skill", "Depth test", "Body.")
	skillDir := filepath.Join(root, "depth-skill")
	rootFile := filepath.Join(skillDir, "root.json")
	subDir := filepath.Join(skillDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := fsskills.NewSourceOptions(fsskills.SourceOptions{SearchDepth: new(1)}, os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	resources := loaded[0].Resources
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource (root only), got %d", len(resources))
	}
	if resources[0].Name != "root.json" {
		t.Fatalf("expected root.json, got %q", resources[0].Name)
	}
}

func TestFileSource_NoDuplicateResourcesFromSamePath(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "dedup-skill", "Dedup test", "Body.")
	refsDir := filepath.Join(root, "dedup-skill", "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "data.md"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	resources := loaded[0].Resources
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].Name != "references/data.md" {
		t.Fatalf("expected references/data.md, got %q", resources[0].Name)
	}
}

func TestFileSource_SymlinkedResource_IsSkipped(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "resource-link-skill", "Symlinked resource", "Body.")
	outsideResource := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outsideResource, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	createSymlink(t, filepath.Join(root, "resource-link-skill", "references", "secret.md"), outsideResource)

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if len(loaded[0].Resources) != 0 {
		t.Fatalf("expected symlinked resource to be skipped, got %d resources", len(loaded[0].Resources))
	}
}

func createSkillDir(t *testing.T, root, name, description, body string) {
	t.Helper()
	skillDir := filepath.Join(root, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func createSkillDirRaw(t *testing.T, root, dirName, rawContent string) {
	t.Helper()
	skillDir := filepath.Join(root, dirName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(rawContent), 0o644); err != nil {
		t.Fatal(err)
	}
}

func createSkillDirWithResource(t *testing.T, root, name, description, body, resourceRelPath, resourceContent string) {
	t.Helper()
	createSkillDir(t, root, name, description, body)
	skillDir := filepath.Join(root, name)
	resourcePath := filepath.Join(skillDir, filepath.FromSlash(resourceRelPath))
	if err := os.MkdirAll(filepath.Dir(resourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resourcePath, []byte(resourceContent), 0o644); err != nil {
		t.Fatal(err)
	}
}

func createRelativeFile(t *testing.T, root, relativePath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func createSymlink(t *testing.T, linkPath, targetPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
}

func TestFileSource_MetadataWithBlankLineCRLF_KeepsAllKeys(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "gap-meta-crlf", strings.Join([]string{
		"---",
		"name: gap-meta-crlf",
		"description: d",
		"metadata:",
		"  a: 1",
		"",
		"  b: 2",
		"  c: 3",
		"---",
		"Body.",
	}, "\r\n"))

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	m := loaded[0].Frontmatter.Metadata
	if m["a"] != "1" || m["b"] != "2" || m["c"] != "3" {
		t.Fatalf("CRLF metadata keys dropped after blank line: %#v", m)
	}
}

func TestFileSource_MetadataWithBlankLine_KeepsAllKeys(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "gap-meta", strings.Join([]string{
		"---",
		"name: gap-meta",
		"description: d",
		"metadata:",
		"  a: 1",
		"",
		"  b: 2",
		"  c: 3",
		"---",
		"Body.",
	}, "\n"))

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	m := loaded[0].Frontmatter.Metadata
	if m["a"] != "1" || m["b"] != "2" || m["c"] != "3" {
		t.Fatalf("metadata keys dropped after blank line: %#v", m)
	}
}

func rawSkillSource(name, content string, opts fsskills.SourceOptions) *fsskills.Source {
	return fsskills.NewSourceOptions(opts, fstest.MapFS{
		name + "/SKILL.md": {Data: []byte(content)},
	})
}

func loadFileSkills(t *testing.T, source *fsskills.Source) []*skills.Skill {
	t.Helper()
	loaded, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestFileSource_ValidSkill_ReturnsNameAndDescription(t *testing.T) {
	source := rawSkillSource("my-skill",
		"---\nname: my-skill\ndescription: A test skill\n---\nUse this skill to do things.", fsskills.SourceOptions{})

	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || loaded[0].Frontmatter.Name != "my-skill" || loaded[0].Frontmatter.Description != "A test skill" {
		t.Fatalf("Skills() = %#v, want the valid skill and its description", loaded)
	}
}

func TestFileSource_QuotedFrontmatterValues_AreParsed(t *testing.T) {
	source := rawSkillSource("quoted-skill",
		"---\nname: 'quoted-skill'\ndescription: \"A quoted description\"\n---\nBody text.", fsskills.SourceOptions{})

	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || loaded[0].Frontmatter.Name != "quoted-skill" || loaded[0].Frontmatter.Description != "A quoted description" {
		t.Fatalf("Skills() = %#v, want unquoted name and description", loaded)
	}
}

func TestFileSource_MissingFrontmatter_ExcludesSkill(t *testing.T) {
	source := rawSkillSource("bad-skill", "No frontmatter here.", fsskills.SourceOptions{})
	if got := loadFileSkills(t, source); len(got) != 0 {
		t.Fatalf("Skills() = %#v, want no skills", got)
	}
}

func TestFileSource_MissingName_ExcludesSkill(t *testing.T) {
	source := rawSkillSource("no-name", "---\ndescription: A skill without a name\n---\nBody.", fsskills.SourceOptions{})
	if got := loadFileSkills(t, source); len(got) != 0 {
		t.Fatalf("Skills() = %#v, want no skills", got)
	}
}

func TestFileSource_MissingDescription_ExcludesSkill(t *testing.T) {
	source := rawSkillSource("no-desc", "---\nname: no-desc\n---\nBody.", fsskills.SourceOptions{})
	if got := loadFileSkills(t, source); len(got) != 0 {
		t.Fatalf("Skills() = %#v, want no skills", got)
	}
}

func TestFileSource_InvalidNames_ExcludeSkill(t *testing.T) {
	for _, name := range []string{"BadName", "-leading-hyphen", "trailing-hyphen-", "has spaces", "consecutive--hyphens"} {
		t.Run(name, func(t *testing.T) {
			source := rawSkillSource(name, "---\nname: "+name+"\ndescription: A skill\n---\nBody.", fsskills.SourceOptions{})
			if got := loadFileSkills(t, source); len(got) != 0 {
				t.Fatalf("Skills() = %#v, want invalid name %q excluded", got, name)
			}
		})
	}
}

func TestFileSource_NameMismatch_ExcludesSkill(t *testing.T) {
	source := rawSkillSource("wrong-dir-name",
		"---\nname: actual-skill-name\ndescription: A skill\n---\nBody.", fsskills.SourceOptions{})
	if got := loadFileSkills(t, source); len(got) != 0 {
		t.Fatalf("Skills() = %#v, want mismatched directory name excluded", got)
	}
}

func TestFileSource_NameTooLong_ExcludesSkill(t *testing.T) {
	source := rawSkillSource("long-name",
		"---\nname: "+strings.Repeat("a", 65)+"\ndescription: A skill\n---\nBody.", fsskills.SourceOptions{})
	if got := loadFileSkills(t, source); len(got) != 0 {
		t.Fatalf("Skills() = %#v, want overlong name excluded", got)
	}
}

func TestFileSource_DescriptionTooLong_ExcludesSkill(t *testing.T) {
	source := rawSkillSource("long-desc",
		"---\nname: long-desc\ndescription: "+strings.Repeat("x", 1025)+"\n---\nBody.", fsskills.SourceOptions{})
	if got := loadFileSkills(t, source); len(got) != 0 {
		t.Fatalf("Skills() = %#v, want overlong description excluded", got)
	}
}

func TestFileSource_Utf8Bom_ParsesNameAndDescription(t *testing.T) {
	source := rawSkillSource("bom-skill",
		"\uFEFF---\nname: bom-skill\ndescription: Skill with BOM\n---\nBody content.", fsskills.SourceOptions{})

	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || loaded[0].Frontmatter.Name != "bom-skill" || loaded[0].Frontmatter.Description != "Skill with BOM" {
		t.Fatalf("Skills() = %#v, want the BOM-prefixed skill", loaded)
	}
}

func TestFileSource_License_IsParsed(t *testing.T) {
	source := rawSkillSource("licensed-skill",
		"---\nname: licensed-skill\ndescription: A skill with license\nlicense: MIT\n---\nBody.", fsskills.SourceOptions{})
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || loaded[0].Frontmatter.License != "MIT" {
		t.Fatalf("Skills() = %#v, want license MIT", loaded)
	}
}

func TestFileSource_Compatibility_IsParsed(t *testing.T) {
	source := rawSkillSource("compat-skill",
		"---\nname: compat-skill\ndescription: A skill with compatibility\ncompatibility: Requires Node.js 18+\n---\nBody.", fsskills.SourceOptions{})
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || loaded[0].Frontmatter.Compatibility != "Requires Node.js 18+" {
		t.Fatalf("Skills() = %#v, want compatibility requirement", loaded)
	}
}

func TestFileSource_AllowedTools_IsParsed(t *testing.T) {
	source := rawSkillSource("tools-skill",
		"---\nname: tools-skill\ndescription: A skill with tools\nallowed-tools: grep glob bash\n---\nBody.", fsskills.SourceOptions{})
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || loaded[0].Frontmatter.AllowedTools != "grep glob bash" {
		t.Fatalf("Skills() = %#v, want allowed tools", loaded)
	}
}

func TestFileSource_Metadata_IsParsed(t *testing.T) {
	source := rawSkillSource("meta-skill",
		"---\nname: meta-skill\ndescription: A skill with metadata\nmetadata:\n  author: test-user\n  version: 1.0\n---\nBody.", fsskills.SourceOptions{})
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || loaded[0].Frontmatter.Metadata["author"] != "test-user" || loaded[0].Frontmatter.Metadata["version"] != "1.0" {
		t.Fatalf("Skills() = %#v, want parsed author and version metadata", loaded)
	}
}

func TestFileSource_ValidExtensionList_Succeeds(t *testing.T) {
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		AllowedResourceExtensions: []string{".md", ".json", ".custom"},
	}, fstest.MapFS{})
	if source == nil {
		t.Fatal("expected source accepting all valid extensions")
	}
}

func TestFileSource_MixedValidAndInvalidExtensions_Panics(t *testing.T) {
	defer func() {
		if got := recover(); got != `invalid extension "json": must start with '.'` {
			t.Fatalf("panic = %v, want invalid json extension", got)
		}
	}()
	fsskills.NewSourceOptions(fsskills.SourceOptions{
		AllowedResourceExtensions: []string{".md", "json"},
	}, fstest.MapFS{})
}

func TestFileSource_MatchingExtensions_DiscoverBothResources(t *testing.T) {
	fsys := fstest.MapFS{
		"resource-skill/SKILL.md":          {Data: []byte("---\nname: resource-skill\ndescription: Has resources\n---\nSee docs for details.")},
		"resource-skill/references/FAQ.md": {Data: []byte("FAQ content")},
		"resource-skill/assets/data.json":  {Data: []byte("{}")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 {
		t.Fatalf("Skills() = %#v, want one skill", loaded)
	}
	names := []string{}
	for _, resource := range loaded[0].Resources {
		names = append(names, resource.Name)
	}
	if len(names) != 2 || !slices.Contains(names, "references/FAQ.md") || !slices.Contains(names, "assets/data.json") {
		t.Fatalf("resource names = %v, want both matching resources", names)
	}
}

func TestFileSource_NonMatchingExtensions_AreExcluded(t *testing.T) {
	fsys := fstest.MapFS{
		"ext-skill/SKILL.md":             {Data: []byte("---\nname: ext-skill\ndescription: Extension test\n---\nBody.")},
		"ext-skill/references/image.png": {Data: []byte("fake image")},
		"ext-skill/references/data.json": {Data: []byte("{}")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "references/data.json" {
		t.Fatalf("Skills() = %#v, want only the JSON resource", loaded)
	}
}

func TestFileSource_SkillMd_IsNotAResource(t *testing.T) {
	fsys := fstest.MapFS{
		"selfref-skill/SKILL.md":            {Data: []byte("---\nname: selfref-skill\ndescription: Self ref test\n---\nBody.")},
		"selfref-skill/references/notes.md": {Data: []byte("notes")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "references/notes.md" {
		t.Fatalf("Skills() = %#v, want only references/notes.md", loaded)
	}
}

func TestFileSource_NestedResourceFiles_OnlyDiscoversWithinDefaultDepth(t *testing.T) {
	fsys := fstest.MapFS{
		"nested-res-skill/SKILL.md":                         {Data: []byte("---\nname: nested-res-skill\ndescription: Nested resources\n---\nBody.")},
		"nested-res-skill/references/top.md":                {Data: []byte("top content")},
		"nested-res-skill/references/level1/level2/deep.md": {Data: []byte("deep content")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "references/top.md" {
		t.Fatalf("Skills() = %#v, want only references/top.md", loaded)
	}
}

func TestFileSource_CustomResourceExtensions_ExcludesDefaultExtensions(t *testing.T) {
	fsys := fstest.MapFS{
		"custom-ext-skill/SKILL.md":               {Data: []byte("---\nname: custom-ext-skill\ndescription: Custom extensions\n---\nBody.")},
		"custom-ext-skill/references/data.custom": {Data: []byte("custom data")},
		"custom-ext-skill/references/data.json":   {Data: []byte("{}")},
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{AllowedResourceExtensions: []string{".custom"}}, fsys)
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "references/data.custom" {
		t.Fatalf("Skills() = %#v, want only the custom resource", loaded)
	}
}

func TestFileSource_RootResources_AreDiscoveredByDefault(t *testing.T) {
	fsys := fstest.MapFS{
		"root-resource-skill/SKILL.md":    {Data: []byte("---\nname: root-resource-skill\ndescription: Root resources\n---\nBody.")},
		"root-resource-skill/guide.md":    {Data: []byte("guide content")},
		"root-resource-skill/config.json": {Data: []byte("{}")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 {
		t.Fatalf("Skills() = %#v, want one skill", loaded)
	}
	names := []string{}
	for _, resource := range loaded[0].Resources {
		names = append(names, resource.Name)
	}
	if len(names) != 2 || !slices.Contains(names, "guide.md") || !slices.Contains(names, "config.json") {
		t.Fatalf("resource names = %v, want both root resources", names)
	}
}

func TestFileSource_AnySubdirectoryResource_IsDiscoveredByDefault(t *testing.T) {
	fsys := fstest.MapFS{
		"non-spec-skill/SKILL.md":       {Data: []byte("---\nname: non-spec-skill\ndescription: Non-spec directory\n---\nBody.")},
		"non-spec-skill/docs/readme.md": {Data: []byte("docs content")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "docs/readme.md" {
		t.Fatalf("Skills() = %#v, want the docs resource", loaded)
	}
}

func TestFileSource_ResourceFilter_ExcludesSelectedDirectory(t *testing.T) {
	fsys := fstest.MapFS{
		"custom-directory-skill/SKILL.md":          {Data: []byte("---\nname: custom-directory-skill\ndescription: Custom directory\n---\nBody.")},
		"custom-directory-skill/docs/readme.md":    {Data: []byte("docs content")},
		"custom-directory-skill/references/ref.md": {Data: []byte("ref content")},
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ResourceFilter: func(ctx fsskills.FilterContext) bool {
			return !strings.HasPrefix(strings.ToLower(ctx.RelativeFilePath), "docs/")
		},
	}, fsys)
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "references/ref.md" {
		t.Fatalf("Skills() = %#v, want only references/ref.md", loaded)
	}
}

func TestFileSource_ResourceFilter_WhitelistsReferences(t *testing.T) {
	fsys := fstest.MapFS{
		"dotslash-res-skill/SKILL.md":             {Data: []byte("---\nname: dotslash-res-skill\ndescription: Dot-slash prefix\n---\nBody.")},
		"dotslash-res-skill/references/data.json": {Data: []byte("{}")},
		"dotslash-res-skill/assets/image.txt":     {Data: []byte("data")},
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ResourceFilter: func(ctx fsskills.FilterContext) bool {
			return strings.HasPrefix(strings.ToLower(ctx.RelativeFilePath), "references/")
		},
	}, fsys)
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "references/data.json" {
		t.Fatalf("Skills() = %#v, want only references/data.json", loaded)
	}
}

func TestFileSource_DepthOne_ExcludesSubdirectoryScripts(t *testing.T) {
	fsys := fstest.MapFS{
		"depth-one-skill/SKILL.md":       {Data: []byte("---\nname: depth-one-skill\ndescription: Depth one\n---\nBody.")},
		"depth-one-skill/scripts/run.py": {Data: []byte("print('hello')")},
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{SearchDepth: new(1)}, fsys)
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || len(loaded[0].Scripts) != 0 {
		t.Fatalf("Skills() = %#v, want subdirectory scripts excluded at depth 1", loaded)
	}
}

func TestFileSource_DeepResource_ExcludedWithDefaultDepth(t *testing.T) {
	fsys := fstest.MapFS{
		"nested-directory-skill/SKILL.md":           {Data: []byte("---\nname: nested-directory-skill\ndescription: Nested directory\n---\nBody.")},
		"nested-directory-skill/f1/f2/f3/data.json": {Data: []byte("{}")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 || len(loaded[0].Resources) != 0 {
		t.Fatalf("Skills() = %#v, want deep resource excluded", loaded)
	}
}

func TestFileSource_DeepResource_DiscoveredWithHigherDepth(t *testing.T) {
	fsys := fstest.MapFS{
		"deep-res-skill/SKILL.md":           {Data: []byte("---\nname: deep-res-skill\ndescription: Deep resource\n---\nBody.")},
		"deep-res-skill/f1/f2/f3/data.json": {Data: []byte("{}")},
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{SearchDepth: new(5)}, fsys)
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "f1/f2/f3/data.json" {
		t.Fatalf("Skills() = %#v, want the deep resource", loaded)
	}
}

func TestFileSource_DuplicateRecognizedFields_ExcludeSkill(t *testing.T) {
	for _, field := range []string{"name", "description", "license", "compatibility", "metadata", "allowed-tools"} {
		t.Run(field, func(t *testing.T) {
			fields := "name: ambiguous-skill\ndescription: A skill\n" + field + ": first\n" + field + ": second"
			switch field {
			case "name":
				fields = "name: ambiguous-skill\nname: ambiguous-skill\ndescription: A skill"
			case "description":
				fields = "name: ambiguous-skill\ndescription: A skill\ndescription: A second description"
			case "metadata":
				fields = "name: ambiguous-skill\ndescription: A skill\nmetadata:\n  author: first\nmetadata:\n  version: 1.0"
			}
			source := rawSkillSource("ambiguous-skill", "---\n"+fields+"\n---\nBody.", fsskills.SourceOptions{})
			if got := loadFileSkills(t, source); len(got) != 0 {
				t.Fatalf("Skills() = %#v, want duplicate %s excluded", got, field)
			}
		})
	}
}

func TestFileSource_IncorrectlyCasedRecognizedFields_ExcludeSkill(t *testing.T) {
	for _, test := range []struct{ field, cased string }{
		{"name", "Name"},
		{"description", "Description"},
		{"license", "License"},
		{"compatibility", "Compatibility"},
		{"metadata", "Metadata"},
		{"allowed-tools", "Allowed-Tools"},
	} {
		t.Run(test.field, func(t *testing.T) {
			fields := "name: ambiguous-skill\ndescription: A skill\n" + test.cased + ": value"
			switch test.field {
			case "name":
				fields = test.cased + ": ambiguous-skill\ndescription: A skill"
			case "description":
				fields = "name: ambiguous-skill\n" + test.cased + ": A skill"
			case "metadata":
				fields = "name: ambiguous-skill\ndescription: A skill\n" + test.cased + ":\n  author: test"
			}
			source := rawSkillSource("ambiguous-skill", "---\n"+fields+"\n---\nBody.", fsskills.SourceOptions{})
			if got := loadFileSkills(t, source); len(got) != 0 {
				t.Fatalf("Skills() = %#v, want incorrectly cased %s excluded", got, test.field)
			}
		})
	}
}

func TestFileSource_ConflictingScalarFormats_ExcludeSkillAndLogError(t *testing.T) {
	firstValues := []string{"First", "'First'", `"First"`, "|-\n  First", ">-\n  First"}
	secondValues := []string{"Second", "'Second'", `"Second"`, "|-\n  Second", ">-\n  Second"}
	for i, first := range firstValues {
		for j, second := range secondValues {
			for _, key := range []string{"description", "Description", "DESCRIPTION"} {
				t.Run(fmt.Sprintf("%d_%d_%s", i, j, key), func(t *testing.T) {
					var logs bytes.Buffer
					source := rawSkillSource("test-skill",
						fmt.Sprintf("---\nname: test-skill\ndescription: %s\n%s: %s\nlicense: MIT\n---\nBody.", first, key, second),
						fsskills.SourceOptions{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
					if got := loadFileSkills(t, source); len(got) != 0 {
						t.Fatalf("Skills() = %#v, want conflicting scalar fields excluded", got)
					}
					diagnostic := "duplicate frontmatter field"
					if key != "description" {
						diagnostic = "incorrectly cased frontmatter field"
					}
					logged := logs.String()
					if strings.Count(logged, `"level":"ERROR"`) != 1 || !strings.Contains(logged, diagnostic) ||
						!strings.Contains(logged, fmt.Sprintf(`"fieldName":%q`, key)) {
						t.Fatalf("expected one field-specific diagnostic, got %s", logged)
					}
					if key != "description" && !strings.Contains(logged, `"expectedFieldName":"description"`) {
						t.Fatalf("missing canonical field name in %s", logged)
					}
				})
			}
		}
	}
}

func TestFileSource_ConflictingQuotedFields_ExcludeSkillAndLogError(t *testing.T) {
	fields := []struct{ key, value string }{
		{"name", "test-skill"},
		{"description", "Read files"},
		{"license", "MIT"},
		{"compatibility", "Any runtime"},
		{"metadata", "\n  author: First"},
		{"allowed-tools", "read"},
	}
	for _, field := range fields {
		var others []string
		for _, other := range fields {
			if other.key != field.key {
				others = append(others, other.key+": "+other.value)
			}
		}
		for _, quote := range []string{"'", `"`} {
			quotedKey := quote + field.key + quote
			quotedField, bareField := quotedKey+": "+field.value, field.key+": "+field.value
			otherQuote := `"`
			if quote == `"` {
				otherQuote = "'"
			}
			declarations := []string{
				quote + strings.ToUpper(field.key) + quote + ": " + field.value,
				quotedField + "\n" + bareField,
				bareField + "\n" + quotedField,
				quotedField + "\n" + quotedField,
				quotedField + "\n" + otherQuote + field.key + otherQuote + ": " + field.value,
				quotedKey + ":\n" + bareField,
				bareField + "\n" + quotedKey + ":",
			}
			for _, newline := range []string{"\n", "\r\n"} {
				for i, declaration := range declarations {
					t.Run(fmt.Sprintf("%s_%q_%q_%d", field.key, quote, newline, i), func(t *testing.T) {
						var logs bytes.Buffer
						content := "---\n" + strings.Join(others, "\n") + "\n" + declaration + "\n---\nBody."
						source := rawSkillSource("test-skill", strings.ReplaceAll(content, "\n", newline),
							fsskills.SourceOptions{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
						if got := loadFileSkills(t, source); len(got) != 0 {
							t.Fatalf("Skills() = %#v, want conflicting quoted fields excluded", got)
						}
						diagnostic, key := "duplicate frontmatter field", field.key
						if i == 0 {
							diagnostic, key = "incorrectly cased frontmatter field", strings.ToUpper(field.key)
						}
						logged := logs.String()
						if strings.Count(logged, `"level":"ERROR"`) != 1 || !strings.Contains(logged, diagnostic) ||
							!strings.Contains(logged, fmt.Sprintf(`"fieldName":%q`, key)) {
							t.Fatalf("expected one field-specific diagnostic, got %s", logged)
						}
						if i == 0 && !strings.Contains(logged, fmt.Sprintf(`"expectedFieldName":%q`, field.key)) {
							t.Fatalf("missing canonical field name in %s", logged)
						}
					})
				}
			}
		}
	}
}

func TestFileSource_NestedMetadataAndUnknownFields_KeepFirstMetadataValue(t *testing.T) {
	const content = "---\nname: test-skill\ndescription: Read files\n" +
		"# description: Not a field\nmetadata:\n  author: First\n  author: Second\n  Description: Nested text\n" +
		"  \"description\": Ignored\n  'metadata': Ignored\nvendor-option: First\nvendor-option: Second\n" +
		"'vendor-option': Third\n\"VENDOR-OPTION\": Fourth\n---\n\"description\": Body text"
	loaded := loadFileSkills(t, rawSkillSource("test-skill", content, fsskills.SourceOptions{}))
	if len(loaded) != 1 {
		t.Fatalf("Skills() = %#v, want one skill", loaded)
	}
	fm := loaded[0].Frontmatter
	if fm.Description != "Read files" || len(fm.Metadata) != 2 || fm.Metadata["author"] != "First" || fm.Metadata["Description"] != "Nested text" {
		t.Fatalf("frontmatter = %#v, want original root description and first metadata values", fm)
	}
}

func TestFileSource_EmptyBlockBeforeAnotherField_ExcludesSkill(t *testing.T) {
	for _, indicator := range []string{"|", "|-", "|+", ">", ">-", ">+"} {
		t.Run(indicator, func(t *testing.T) {
			content := "---\nname: test-skill\ndescription: " + indicator + "\n\nlicense: MIT\n---\nBody."
			source := rawSkillSource("test-skill", content, fsskills.SourceOptions{})
			if got := loadFileSkills(t, source); len(got) != 0 {
				t.Fatalf("Skills() = %#v, want empty block description excluded", got)
			}
		})
	}
}

func TestFileSource_QuotedEmptyOptionalFields_RemainUnset(t *testing.T) {
	for _, quote := range []string{"'", `"`} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%q_%q", quote, newline), func(t *testing.T) {
				content := fmt.Sprintf("---\n%[1]smetadata%[1]s:\n%[1]sname%[1]s: test-skill\n"+
					"%[1]sdescription%[1]s: Read files\n%[1]slicense%[1]s:\n"+
					"%[1]scompatibility%[1]s:\n%[1]sallowed-tools%[1]s:\n---\nBody.", quote)
				loaded := loadFileSkills(t, rawSkillSource("test-skill", strings.ReplaceAll(content, "\n", newline), fsskills.SourceOptions{}))
				if len(loaded) != 1 {
					t.Fatalf("Skills() = %#v, want one skill", loaded)
				}
				fm := loaded[0].Frontmatter
				if fm.Name != "test-skill" || fm.Description != "Read files" || fm.Metadata != nil || fm.License != "" || fm.Compatibility != "" || fm.AllowedTools != "" {
					t.Fatalf("frontmatter = %#v, want required fields and unset optionals", fm)
				}
			})
		}
	}
}

func TestFileSource_EmptyInlineValue_DoesNotConsumeNextField(t *testing.T) {
	for _, field := range []string{"metadata", "license", "compatibility", "allowed-tools", "vendor-option"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%s_%q", field, newline), func(t *testing.T) {
				content := "---\n" + field + ": \t\nname: test-skill\ndescription: A test skill.\n---\nBody."
				loaded := loadFileSkills(t, rawSkillSource("test-skill", strings.ReplaceAll(content, "\n", newline), fsskills.SourceOptions{}))
				if len(loaded) != 1 {
					t.Fatalf("Skills() = %#v, want one skill", loaded)
				}
				fm := loaded[0].Frontmatter
				if fm.Name != "test-skill" || fm.Description != "A test skill." || fm.License != "" || fm.Compatibility != "" || fm.AllowedTools != "" {
					t.Fatalf("empty optional value consumed a following field: %#v", fm)
				}
			})
		}
	}
}

func TestFileSource_EmptyOptionalScalarAtEnd_RemainsUnset(t *testing.T) {
	for _, field := range []string{"license", "compatibility", "allowed-tools"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%s_%q", field, newline), func(t *testing.T) {
				content := "---\nname: test-skill\ndescription: Read files\n" + field + ": \t\n---\nBody."
				loaded := loadFileSkills(t, rawSkillSource("test-skill", strings.ReplaceAll(content, "\n", newline), fsskills.SourceOptions{}))
				if len(loaded) != 1 {
					t.Fatalf("Skills() = %#v, want one skill", loaded)
				}
				fm := loaded[0].Frontmatter
				if fm.Description != "Read files" || fm.License != "" || fm.Compatibility != "" || fm.AllowedTools != "" {
					t.Fatalf("empty trailing optional value was not preserved: %#v", fm)
				}
			})
		}
	}
}

func TestFileSource_EmptyInlineValue_DoesNotBypassKeyValidation(t *testing.T) {
	fields := []string{
		"name:", "description:", "metadata:\nmetadata:", "license:\nlicense: MIT", "license: MIT\nlicense:",
		"compatibility:\ncompatibility: Any runtime", "allowed-tools:\nallowed-tools: read",
		"Metadata:", "allowed-tools: read\nALLOWED-TOOLS:",
	}
	for i, field := range fields {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%d_%q", i, newline), func(t *testing.T) {
				content := "---\n" + field + "\nname: test-skill\ndescription: A test skill.\n---\nBody."
				source := rawSkillSource("test-skill", strings.ReplaceAll(content, "\n", newline), fsskills.SourceOptions{})
				if got := loadFileSkills(t, source); len(got) != 0 {
					t.Fatalf("Skills() = %#v, want empty fields validated before being ignored", got)
				}
			})
		}
	}
}

func TestFileSource_LinkedSkillDirectory_SkipsLinkedSkill(t *testing.T) {
	fsys := fstest.MapFS{
		"root/good-skill/SKILL.md":    {Data: []byte("---\nname: good-skill\ndescription: A skill\n---\nBody.")},
		"outside/evil-skill/SKILL.md": {Data: []byte("---\nname: evil-skill\ndescription: Linked skill\n---\nBody.")},
		"root/evil-skill":             {Mode: fs.ModeSymlink, Data: []byte("../outside/evil-skill")},
	}
	root, err := fs.Sub(fsys, "root")
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadFileSkills(t, fsskills.NewSource(root))
	if len(loaded) != 1 || loaded[0].Frontmatter.Name != "good-skill" {
		t.Fatalf("Skills() = %#v, want only good-skill", loaded)
	}
}

func TestFileSource_LinkInResourcePath_SkipsLinkedResources(t *testing.T) {
	fsys := fstest.MapFS{
		"root/symlink-escape-skill/SKILL.md":        {Data: []byte("---\nname: symlink-escape-skill\ndescription: Symlinked directory escape\n---\nBody.")},
		"root/symlink-escape-skill/assets/legit.md": {Data: []byte("legit content")},
		"root/symlink-escape-skill/references":      {Mode: fs.ModeSymlink, Data: []byte("../../outside")},
		"outside/secret.md":                         {Data: []byte("secret content")},
	}
	root, err := fs.Sub(fsys, "root")
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadFileSkills(t, fsskills.NewSource(root))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "assets/legit.md" {
		t.Fatalf("Skills() = %#v, want only the legitimate asset", loaded)
	}
}

func TestFileSource_LinkedResourceDirectory_SkipsExternalFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"root/symlink-directory-skip/SKILL.md":        {Data: []byte("---\nname: symlink-directory-skip\ndescription: Symlinked directory skip\n---\nBody.")},
		"root/symlink-directory-skip/assets/legit.md": {Data: []byte("legit content")},
		"root/symlink-directory-skip/references":      {Mode: fs.ModeSymlink, Data: []byte("../../outside-resources")},
		"outside-resources/external.md":               {Data: []byte("external content")},
		"outside-resources/data.json":                 {Data: []byte("{}")},
	}
	root, err := fs.Sub(fsys, "root")
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadFileSkills(t, fsskills.NewSource(root))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "assets/legit.md" {
		t.Fatalf("Skills() = %#v, want external resource directory excluded", loaded)
	}
}

func TestFileSource_LinkedScriptDirectory_SkipsExternalScripts(t *testing.T) {
	fsys := fstest.MapFS{
		"root/symlink-script-skip/SKILL.md": {Data: []byte("---\nname: symlink-script-skip\ndescription: Symlinked script directory\n---\nBody.")},
		"root/symlink-script-skip/scripts":  {Mode: fs.ModeSymlink, Data: []byte("../../outside-scripts")},
		"outside-scripts/malicious.py":      {Data: []byte("print('external')")},
	}
	root, err := fs.Sub(fsys, "root")
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadFileSkills(t, fsskills.NewSource(root))
	if len(loaded) != 1 || len(loaded[0].Scripts) != 0 {
		t.Fatalf("Skills() = %#v, want external scripts excluded", loaded)
	}
}

func TestFileSource_LinkedIntermediateDirectory_SkipsDeepResources(t *testing.T) {
	fsys := fstest.MapFS{
		"root/symlink-intermediate/SKILL.md":     {Data: []byte("---\nname: symlink-intermediate\ndescription: Intermediate symlink\n---\nBody.")},
		"root/symlink-intermediate/sub":          {Mode: fs.ModeSymlink, Data: []byte("../../outside-intermediate")},
		"outside-intermediate/resources/data.md": {Data: []byte("data")},
	}
	root, err := fs.Sub(fsys, "root")
	if err != nil {
		t.Fatal(err)
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{SearchDepth: new(4)}, root)
	loaded := loadFileSkills(t, source)
	if len(loaded) != 1 || len(loaded[0].Resources) != 0 {
		t.Fatalf("Skills() = %#v, want linked intermediate directory excluded", loaded)
	}
}

func TestFileSource_LinkedFileInRealDirectory_SkipsOnlyLinkedFile(t *testing.T) {
	fsys := fstest.MapFS{
		"root/symlink-file-skill/SKILL.md":            {Data: []byte("---\nname: symlink-file-skill\ndescription: Symlinked file\n---\nBody.")},
		"root/symlink-file-skill/references/legit.md": {Data: []byte("legit content")},
		"root/symlink-file-skill/references/leak.md":  {Mode: fs.ModeSymlink, Data: []byte("../../../outside-file/secret.md")},
		"outside-file/secret.md":                      {Data: []byte("secret content")},
	}
	root, err := fs.Sub(fsys, "root")
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadFileSkills(t, fsskills.NewSource(root))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 || loaded[0].Resources[0].Name != "references/legit.md" {
		t.Fatalf("Skills() = %#v, want only the real file", loaded)
	}
}

func TestFileSource_ReadResource_RejectsReplacedSymlink(t *testing.T) {
	fsys := fstest.MapFS{
		"read-symlink-skill/SKILL.md":          {Data: []byte("---\nname: read-symlink-skill\ndescription: A skill\n---\nSee docs.")},
		"read-symlink-skill/references/doc.md": {Data: []byte("Safe content.")},
		"secret.md":                            {Data: []byte("Secret content.")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 {
		t.Fatalf("Skills() = %#v, want one resource", loaded)
	}
	resource := loaded[0].Resources[0]
	fsys["read-symlink-skill/references/doc.md"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("../../secret.md")}

	if got, err := resource.Read(t.Context()); err == nil || !strings.Contains(err.Error(), "symbolic link") || got != nil {
		t.Fatalf("Read() = %#v, %v, want rejected replacement link", got, err)
	}
}

func TestFileSource_ReadResource_RejectsReplacedSkillDirectory(t *testing.T) {
	fsys := fstest.MapFS{
		"swapped-skill/SKILL.md": {Data: []byte("---\nname: swapped-skill\ndescription: A skill\n---\nSee docs.")},
		"swapped-skill/doc.md":   {Data: []byte("Safe content.")},
		"decoy/doc.md":           {Data: []byte("Attacker content.")},
	}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 || len(loaded[0].Resources) != 1 {
		t.Fatalf("Skills() = %#v, want one resource", loaded)
	}
	resource := loaded[0].Resources[0]
	delete(fsys, "swapped-skill/SKILL.md")
	delete(fsys, "swapped-skill/doc.md")
	fsys["swapped-skill"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("decoy")}

	if got, err := resource.Read(t.Context()); err == nil || !strings.Contains(err.Error(), "symbolic link") || got != nil {
		t.Fatalf("Read() = %#v, %v, want rejected replacement directory", got, err)
	}
}

type unreadableSkillDirectoryFS struct {
	fstest.MapFS
	listAttempts int
}

func (f *unreadableSkillDirectoryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "blocked" {
		f.listAttempts++
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadDir(name)
}

func TestFileSource_UnreadableSubdirectory_StillDiscoversSibling(t *testing.T) {
	fsys := &unreadableSkillDirectoryFS{MapFS: fstest.MapFS{
		"blocked":             {Mode: fs.ModeDir},
		"good-skill/SKILL.md": {Data: []byte("---\nname: good-skill\ndescription: A skill\n---\nBody.")},
	}}
	loaded := loadFileSkills(t, fsskills.NewSource(fsys))
	if len(loaded) != 1 || loaded[0].Frontmatter.Name != "good-skill" {
		t.Fatalf("Skills() = %#v, want good-skill despite inaccessible sibling", loaded)
	}
	if fsys.listAttempts != 1 {
		t.Fatalf("blocked directory listing attempts = %d, want 1", fsys.listAttempts)
	}
}
