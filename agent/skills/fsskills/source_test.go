// Copyright (c) Microsoft. All rights reserved.

package fsskills_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
)

type fsWithoutLinkInspection struct {
	fs.FS
}

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

func TestFileSkill_WithoutResources_ContentIncludesEmptyAvailableResourcesBlock(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "no-resource-content", "A skill", "No resources here.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
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
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := loaded[0].GetContent(t.Context())
	if err != nil {
		t.Fatal(err)
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
	loaded, err := source.Skills(t.Context())
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
	outsideSkillFile := filepath.Join(root, "outside-SKILL.md")
	if err := os.WriteFile(outsideSkillFile, []byte("---\nname: linked-skill\ndescription: Linked skill file\n---\nBody."), 0o644); err != nil {
		t.Fatal(err)
	}
	createSymlink(t, filepath.Join(skillDir, "SKILL.md"), outsideSkillFile)

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context())
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

func TestFileSource_ConfiguredRootSymlink_StillDiscoversSkills(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real-root")
	linkedRoot := filepath.Join(root, "linked-root")
	createSkillDir(t, realRoot, "my-skill", "A skill", "Body.")
	createSymlink(t, linkedRoot, realRoot)

	source := fsskills.NewSource(os.DirFS(linkedRoot))
	loaded, err := source.Skills(t.Context())
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
	loaded, err := source.Skills(t.Context())
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

func TestFileSource_SearchDepth_DoesNotAffectSkillDirectoryDiscovery(t *testing.T) {
	root := t.TempDir()
	// A skill directory nested four levels below the filesystem root.
	createSkillDir(t, filepath.Join(root, "l1", "l2", "l3"), "deep-skill", "Deep", "Body.")

	// SearchDepth governs only within-skill resource/script discovery; it must
	// not widen skill-directory discovery, which is bounded independently. Even
	// a large SearchDepth leaves the deeply-nested skill directory undiscovered.
	deep := fsskills.NewSourceOptions(fsskills.SourceOptions{SearchDepth: new(4)}, os.DirFS(root))
	loaded, err := deep.Skills(t.Context())
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

func TestFileSource_ReadResource_RevalidatesParentDirectoriesBeforeUse(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, filepath.Join(root, "trusted"), "read-skill", "A skill", "See docs.", "references/doc.md", "trusted content")
	createSkillDirWithResource(t, filepath.Join(root, "outside", "trusted"), "read-skill", "A skill", "See docs.", "references/doc.md", "outside content")

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context())
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

	loaded, err := source.Skills(t.Context())
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

	loaded, err := source.Skills(t.Context())
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
			loaded, err := source.Skills(t.Context())
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

	loaded, err := source.Skills(t.Context())
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

	loaded, err := source.Skills(t.Context())
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

	loaded, err := source.Skills(t.Context())
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

	source := fsskills.NewSourceOptions(fsskills.SourceOptions{SearchDepth: new(1)}, os.DirFS(root))
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

func TestFileSource_SymlinkedResource_IsSkipped(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "resource-link-skill", "Symlinked resource", "Body.")
	outsideResource := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outsideResource, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	createSymlink(t, filepath.Join(root, "resource-link-skill", "references", "secret.md"), outsideResource)

	source := fsskills.NewSource(os.DirFS(root))
	loaded, err := source.Skills(t.Context())
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
	loaded, err := source.Skills(t.Context())
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
	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m := loaded[0].Frontmatter.Metadata
	if m["a"] != "1" || m["b"] != "2" || m["c"] != "3" {
		t.Fatalf("metadata keys dropped after blank line: %#v", m)
	}
}
