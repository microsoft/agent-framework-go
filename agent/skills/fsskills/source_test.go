// Copyright (c) Microsoft. All rights reserved.

package fsskills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
)

func TestFileSource_EmptyPaths_ReturnsEmptyList(t *testing.T) {
	source := fsskills.NewSource()

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected no skills, got %d", len(loaded))
	}
}

func TestFileSource_NonExistentPath_ReturnsEmptyList(t *testing.T) {
	root := t.TempDir()
	source := fsskills.NewSource(os.DirFS(filepath.Join(root, "does-not-exist")))

	loaded, err := source.Skills(t.Context())
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

	loaded, err := source.Skills(t.Context())
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

func TestFileSource_NestedSkillDirectory_DiscoveredWithinDepthLimit(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, filepath.Join(root, "level1"), "nested-skill", "Nested", "Nested body.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
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

func TestFileSource_SkillBeyondMaxDepth_NotDiscovered(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, filepath.Join(root, "l1", "l2", "l3"), "deep-skill", "Too deep", "Body.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range loaded {
		if skill.Frontmatter.Name == "deep-skill" {
			t.Fatal("expected deep-skill not to be discovered beyond max search depth")
		}
	}
}

func TestFileSource_ReadResource_ValidResource_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "read-skill", "A skill", "See docs.", "references/doc.md", "Document content here.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := loaded[0].Resources[0].Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if content != "Document content here." {
		t.Fatalf("expected resource content, got %q", content)
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

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
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

	loaded, err := source.Skills(t.Context())
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

	loaded, err := source.Skills(t.Context())
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

	loaded, err := source.Skills(t.Context())
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

			loaded, err := source.Skills(t.Context())
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
	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	fm := loaded[0].Frontmatter
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

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fm := loaded[0].Frontmatter
	if fm.License != "" || fm.Compatibility != "" || fm.AllowedTools != "" {
		t.Fatalf("expected zero-value optional fields, got %#v", fm)
	}
	if fm.Metadata != nil {
		t.Fatalf("expected nil metadata, got %#v", fm.Metadata)
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
	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
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

	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ResourceFilter: func(ctx fsskills.FilterContext) bool {
			return ctx.RelativeFilePath == "references/keep.json"
		},
	}, os.DirFS(root))
	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resources := loaded[0].Resources
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d; resources: %v", len(resources), resources)
	}
	if resources[0].Name != "references/keep.json" {
		t.Fatalf("expected references/keep.json, got %q", resources[0].Name)
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

	source := fsskills.NewSourceOptions(fsskills.SourceOptions{SearchDepth: 1}, os.DirFS(root))
	loaded, err := source.Skills(t.Context())
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
	if err := os.WriteFile(filepath.Join(refsDir, "data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resources := loaded[0].Resources
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].Name != "references/data.json" {
		t.Fatalf("expected references/data.json, got %q", resources[0].Name)
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
