// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/memory/skills"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

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

func newProvider(t *testing.T, roots ...string) *memory.ContextProvider {
	t.Helper()
	fsList := make([]fs.FS, len(roots))
	for i, r := range roots {
		fsList[i] = os.DirFS(r)
	}
	return skills.New(nil, fsList...)
}

func captureProviderContext(t *testing.T, provider *memory.ContextProvider) (string, []tool.Tool) {
	t.Helper()
	ctx := context.Background()
	out, err := provider.BeforeRun(memory.BeforeRunContext{Context: ctx,
		Session:  memory.NewSession(""),
		Messages: nil,
		Tools:    nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) == 0 {
		return "", out.Tools
	}
	for _, content := range out.Messages[0].Contents {
		if txt, ok := content.(*message.TextContent); ok {
			return txt.Text, out.Tools
		}
	}
	return "", out.Tools
}

func findTool(t *testing.T, tools []tool.Tool, name string) tool.FuncTool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name() == name {
			ft, ok := tl.(tool.FuncTool)
			if !ok {
				t.Fatalf("tool %q does not implement FuncTool", name)
			}
			return ft
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func hasSkill(t *testing.T, provider *memory.ContextProvider, name string) bool {
	t.Helper()
	instructions, _ := captureProviderContext(t, provider)
	return strings.Contains(instructions, name)
}

func TestDiscovery_ValidSkill(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "my-skill", "A test skill", "Use this skill to do things.")

	p := newProvider(t, root)
	if !hasSkill(t, p, "my-skill") {
		t.Fatal("expected skill 'my-skill' to be discovered")
	}
	if !hasSkill(t, p, "A test skill") {
		t.Fatal("expected description in instructions")
	}
}

func TestDiscovery_QuotedFrontmatterValues(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "quoted-skill",
		"---\nname: 'quoted-skill'\ndescription: \"A quoted description\"\n---\nBody text.")

	p := newProvider(t, root)
	if !hasSkill(t, p, "quoted-skill") {
		t.Fatal("expected quoted-skill to be discovered")
	}
	if !hasSkill(t, p, "A quoted description") {
		t.Fatal("expected quoted description in instructions")
	}
}

func TestDiscovery_MissingFrontmatter_Excluded(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "bad-skill", "No frontmatter here.")

	p := newProvider(t, root)
	instructions, tools := captureProviderContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected no skills when frontmatter is missing")
	}
}

func TestDiscovery_MissingNameField_Excluded(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "no-name", "---\ndescription: A skill without a name\n---\nBody.")

	p := newProvider(t, root)
	instructions, tools := captureProviderContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected no skills when name is missing")
	}
}

func TestDiscovery_MissingDescriptionField_Excluded(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "no-desc", "---\nname: no-desc\n---\nBody.")

	p := newProvider(t, root)
	instructions, tools := captureProviderContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected no skills when description is missing")
	}
}

func TestDiscovery_ConsecutiveHyphensInName_Excluded(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "bad-hyphen-name",
		"---\nname: my--skill\ndescription: Invalid hyphen pattern\n---\nBody.")

	p := newProvider(t, root)
	instructions, tools := captureProviderContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected skill with consecutive hyphens in name to be excluded")
	}
}

func TestDiscovery_NameMustMatchDirectory_ExcludedOnMismatch(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "dir-name",
		"---\nname: other-name\ndescription: Mismatch\n---\nBody.")

	p := newProvider(t, root)
	if hasSkill(t, p, "other-name") {
		t.Fatal("expected skill to be excluded when frontmatter name mismatches directory name")
	}
}

func TestDiscovery_NameExceedsMaxLength_Excluded(t *testing.T) {
	root := t.TempDir()
	longName := strings.Repeat("a", 65)
	createSkillDirRaw(t, root, "long-name",
		"---\nname: "+longName+"\ndescription: A skill\n---\nBody.")

	p := newProvider(t, root)
	instructions, tools := captureProviderContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected skill with long name to be excluded")
	}
}

func TestDiscovery_DescriptionExceedsMaxLength_Excluded(t *testing.T) {
	root := t.TempDir()
	longDesc := strings.Repeat("x", 1025)
	createSkillDirRaw(t, root, "long-desc",
		"---\nname: long-desc\ndescription: "+longDesc+"\n---\nBody.")

	p := newProvider(t, root)
	instructions, tools := captureProviderContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected skill with long description to be excluded")
	}
}

func TestDiscovery_FileWithUtf8Bom(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "bom-skill",
		"\uFEFF---\nname: bom-skill\ndescription: Skill with BOM\n---\nBody content.")

	p := newProvider(t, root)
	if !hasSkill(t, p, "bom-skill") {
		t.Fatal("expected BOM skill to be discovered")
	}
}

func TestDiscovery_PathTraversal_Excluded(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "traversal-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: traversal-skill\ndescription: Traversal attempt\n---\nSee [doc](../secret.txt)."), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProvider(t, root)
	if hasSkill(t, p, "traversal-skill") {
		t.Fatal("expected traversal skill to be excluded")
	}
}

func TestProvider_WithSkills_ReturnsInstructionsAndTools(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "provider-skill", "Provider skill test", "Skill instructions body.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	toolNames := make(map[string]bool)
	for _, tl := range tools {
		toolNames[tl.Name()] = true
	}
	if !toolNames["load_skill"] {
		t.Error("expected load_skill tool")
	}
	if !toolNames["read_skill_resource"] {
		t.Error("expected read_skill_resource tool")
	}
}

func TestProvider_CustomPromptTemplate(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "custom-prompt-skill", "Custom prompt", "Body.")

	p := skills.New(&skills.Config{SkillsInstructionPrompt: "Custom template: %s"}, os.DirFS(root))

	instructions, _ := captureProviderContext(t, p)
	if !strings.HasPrefix(instructions, "Custom template:") {
		t.Errorf("expected custom template prefix, got %q", instructions)
	}
}

func TestProvider_SkillNamesAreXmlEscaped(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "xml-skill",
		"---\nname: xml-skill\ndescription: Uses <tags> & \"quotes\"\n---\nBody.")

	p := newProvider(t, root)
	instructions, _ := captureProviderContext(t, p)
	if !strings.Contains(instructions, "&lt;tags&gt;") {
		t.Error("expected XML-escaped angle brackets")
	}
	if !strings.Contains(instructions, "&amp;") {
		t.Error("expected XML-escaped ampersand")
	}
}

func TestProvider_MultiplePaths(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, filepath.Join(root, "dir1"), "skill-a", "Skill A", "Body A.")
	createSkillDir(t, filepath.Join(root, "dir2"), "skill-b", "Skill B", "Body B.")

	p := newProvider(t, filepath.Join(root, "dir1"), filepath.Join(root, "dir2"))
	if !hasSkill(t, p, "skill-a") {
		t.Error("expected skill-a")
	}
	if !hasSkill(t, p, "skill-b") {
		t.Error("expected skill-b")
	}
}

func TestProvider_SkillsListIsSortedByName(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "zulu-skill", "Zulu skill", "Body Z.")
	createSkillDir(t, root, "alpha-skill", "Alpha skill", "Body A.")
	createSkillDir(t, root, "mike-skill", "Mike skill", "Body M.")

	p := newProvider(t, root)
	instructions, _ := captureProviderContext(t, p)

	alphaIdx := strings.Index(instructions, "alpha-skill")
	mikeIdx := strings.Index(instructions, "mike-skill")
	zuluIdx := strings.Index(instructions, "zulu-skill")
	if alphaIdx >= mikeIdx {
		t.Error("alpha-skill should appear before mike-skill")
	}
	if mikeIdx >= zuluIdx {
		t.Error("mike-skill should appear before zulu-skill")
	}
}

func TestLoadSkill_ReturnsBody(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "load-test", "A skill", "Full instructions here.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	loadTool := findTool(t, tools, "load_skill")

	result, err := loadTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"load-test"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Full instructions here." {
		t.Errorf("expected body, got %q", result)
	}
}

func TestLoadSkill_NotFound(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "some-skill", "A skill", "Body.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	loadTool := findTool(t, tools, "load_skill")

	result, err := loadTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"nonexistent"}`)
	if err != nil {
		t.Fatalf("expected no tool error, got %v", err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.HasPrefix(resultStr, "Error:") {
		t.Fatalf("expected error text result, got %q", resultStr)
	}
}

func TestReadResource_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "res-test", "A skill",
		"See [doc](refs/doc.md).", "refs/doc.md", "Resource content here.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"res-test","resourceName":"refs/doc.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Resource content here." {
		t.Errorf("expected resource content, got %q", result)
	}
}

func TestReadResource_DotSlashPrefix(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "dotslash-read", "A skill",
		"See [doc](refs/doc.md).", "refs/doc.md", "Document content.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"dotslash-read","resourceName":"./refs/doc.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Document content." {
		t.Errorf("expected 'Document content.', got %q", result)
	}
}

func TestReadResource_UnregisteredResource(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "simple-skill", "A skill", "No resources.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"simple-skill","resourceName":"unknown.md"}`)
	if err != nil {
		t.Fatalf("expected no tool error, got %v", err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.HasPrefix(resultStr, "Error:") {
		t.Fatalf("expected error text result, got %q", resultStr)
	}
}
