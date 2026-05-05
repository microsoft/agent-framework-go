// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
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

func newProvider(t *testing.T, roots ...string) *agent.ContextProvider {
	t.Helper()
	return newProviderWithConfig(t, nil, nil, roots...)
}

func newProviderWithConfig(t *testing.T, sourceOptions *fsskills.SourceOptions, providerOptions *skills.ContextProviderOptions, roots ...string) *agent.ContextProvider {
	t.Helper()
	if sourceOptions == nil {
		sourceOptions = &fsskills.SourceOptions{}
	}
	fsList := make([]fs.FS, len(roots))
	for i, r := range roots {
		fsList[i] = os.DirFS(r)
	}
	source := fsskills.NewSourceOptions(*sourceOptions, fsList...)
	resolved := skills.ContextProviderOptions{}
	if providerOptions != nil {
		resolved = *providerOptions
	}
	resolved.Sources = []skills.Source{source}
	return skills.NewContextProvider(resolved)
}

func captureProviderContext(t *testing.T, provider *agent.ContextProvider) (string, []tool.Tool) {
	t.Helper()
	ctx := context.Background()
	messages, options, err := provider.BeforeRun(ctx, nil, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatal(err)
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	instructions, _ := agent.GetOption(options, agent.WithInstructions)
	if len(messages) != 0 {
		t.Fatalf("expected skills provider not to add messages, got %d", len(messages))
	}
	return instructions, tools
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

func hasTool(tools []tool.Tool, name string) bool {
	for _, tl := range tools {
		if tl.Name() == name {
			return true
		}
	}
	return false
}

func hasSkill(t *testing.T, provider *agent.ContextProvider, name string) bool {
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

func TestDiscovery_ResourceOutsideConfiguredDirectory_NotDiscoverable(t *testing.T) {
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

	// Skill is discovered (body content doesn't affect discovery),
	// but ../secret.txt is not in any configured resource directory.
	p := newProvider(t, root)
	if !hasSkill(t, p, "traversal-skill") {
		t.Fatal("expected skill to be discovered")
	}
	_, tools := captureProviderContext(t, p)
	if hasTool(tools, "read_skill_resource") {
		t.Fatal("expected no read_skill_resource tool when no resources were discovered")
	}
}

func TestProvider_WithSkills_ReturnsInstructionsAndTools(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "provider-skill", "Provider skill test", "Skill instructions body.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	toolNames := make(map[string]bool)
	for _, tl := range tools {
		toolNames[tl.Name()] = true
	}
	if !toolNames["load_skill"] {
		t.Error("expected load_skill tool")
	}
	if toolNames["read_skill_resource"] {
		t.Error("did not expect read_skill_resource tool without discovered resources")
	}
}

func TestProvider_CustomPromptTemplate(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "custom-prompt-skill", "Custom prompt", "Body.")

	p := newProviderWithConfig(t, nil, &skills.ContextProviderOptions{SkillsInstructionPrompt: "Custom template:\n{skills}\n{resource_instructions}\n{script_instructions}"}, root)

	instructions, _ := captureProviderContext(t, p)
	if !strings.HasPrefix(instructions, "Custom template:") {
		t.Errorf("expected custom template prefix, got %q", instructions)
	}
}

func TestProvider_DefaultPrompt_UsesDotNetResourceGuidance(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "resource-guidance", "Resource guidance", "See [guide](references/FAQ.md).", "references/FAQ.md", "Guidance")

	p := newProvider(t, root)
	instructions, _ := captureProviderContext(t, p)
	if !strings.Contains(instructions, "- Use `read_skill_resource` to read any referenced resources, using the name exactly as listed") {
		t.Fatalf("expected exact-name guidance, got %q", instructions)
	}
	if !strings.Contains(instructions, "(e.g. `\"style-guide\"` not `\"style-guide.md\"`, `\"references/FAQ.md\"` not `\"FAQ.md\"`).") {
		t.Fatalf("expected .NET-aligned resource examples, got %q", instructions)
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
	expected := "---\nname: load-test\ndescription: A skill\n---\nFull instructions here."
	if result != expected {
		t.Errorf("expected full SKILL.md content, got %q", result)
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

func TestLoadSkill_RequiresExactName(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "exact-skill", "A skill", "Body.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	loadTool := findTool(t, tools, "load_skill")

	result, err := loadTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"Exact-Skill"}`)
	if err != nil {
		t.Fatalf("expected no tool error, got %v", err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if resultStr != "Error: Skill 'Exact-Skill' not found." {
		t.Fatalf("expected exact-name error, got %q", resultStr)
	}
}

func TestReadResource_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "res-test", "A skill",
		"See [doc](references/doc.md).", "references/doc.md", "Resource content here.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"res-test","resourceName":"references/doc.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Resource content here." {
		t.Errorf("expected resource content, got %#v", result)
	}
}

func TestReadResource_RequiresExactName(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "dotslash-read", "A skill",
		"See [doc](references/doc.md).", "references/doc.md", "Document content.")

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"dotslash-read","resourceName":"./references/doc.md"}`)
	if err != nil {
		t.Fatalf("expected no tool error, got %v", err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if resultStr != "Error: Resource './references/doc.md' not found in skill 'dotslash-read'." {
		t.Fatalf("expected exact-name error, got %q", resultStr)
	}
}

func TestReadResource_UnregisteredResource(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "simple-skill", "A skill", "See [doc](references/doc.md).", "references/doc.md", "Document content.")

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

func TestDiscovery_ResourcesInDefaultDirectories(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "res-skill", "Resource skill", "Body.")
	skillDir := filepath.Join(root, "res-skill")

	// Create resources in both default directories
	refsDir := filepath.Join(skillDir, "references")
	assetsDir := filepath.Join(skillDir, "assets")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "FAQ.md"), []byte("FAQ content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"res-skill","resourceName":"references/FAQ.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "FAQ content" {
		t.Errorf("expected FAQ content, got %#v", result)
	}

	result, err = readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"res-skill","resourceName":"assets/data.json"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "{}" {
		t.Errorf("expected {}, got %#v", result)
	}
}

func TestDiscovery_ResourceInNonSpecDirectory_NotDiscoveredByDefault(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "non-spec-skill", "Non-spec directory", "Body.")
	skillDir := filepath.Join(root, "non-spec-skill")

	// Create resource in a non-default directory
	docsDir := filepath.Join(skillDir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "readme.md"), []byte("docs content"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProvider(t, root)
	if !hasSkill(t, p, "non-spec-skill") {
		t.Fatal("expected skill to be discovered")
	}
	_, tools := captureProviderContext(t, p)
	if hasTool(tools, "read_skill_resource") {
		t.Fatal("expected no read_skill_resource tool when only non-default resource directories exist")
	}
}

func TestDiscovery_ResourceInSkillRoot_NotDiscoveredByDefault(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "root-resource-skill", "Root resource", "Body.")
	skillDir := filepath.Join(root, "root-resource-skill")

	if err := os.WriteFile(filepath.Join(skillDir, "guide.md"), []byte("guide content"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	if hasTool(tools, "read_skill_resource") {
		t.Fatal("expected no read_skill_resource tool when only root-level resources exist and '.' is not configured")
	}
}

func TestDiscovery_ResourceInSkillRoot_DiscoveredWhenRootDirectoryConfigured(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "root-opt-in-skill", "Root opt-in", "Body.")
	skillDir := filepath.Join(root, "root-opt-in-skill")

	if err := os.WriteFile(filepath.Join(skillDir, "guide.md"), []byte("guide content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProviderWithConfig(t, &fsskills.SourceOptions{
		ResourceDirectories: []string{"references", "assets", "."},
	}, nil, root)

	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"root-opt-in-skill","resourceName":"guide.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "guide content" {
		t.Errorf("expected guide content, got %#v", result)
	}

	result, err = readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"root-opt-in-skill","resourceName":"config.json"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "{}" {
		t.Errorf("expected {}, got %#v", result)
	}
}

func TestDiscovery_SkillMdNotIncludedAsResource(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "selfref-skill", "Self ref test", "Body.")

	// Opt into root scanning — SKILL.md should still be excluded
	p := newProviderWithConfig(t, &fsskills.SourceOptions{
		ResourceDirectories: []string{"."},
	}, nil, root)

	_, tools := captureProviderContext(t, p)
	if hasTool(tools, "read_skill_resource") {
		t.Fatal("expected no read_skill_resource tool when the only root-level file is SKILL.md")
	}
}

func TestDiscovery_CustomResourceDirectories_ReplacesDefaults(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "custom-directory-skill", "Custom directory", "Body.")
	skillDir := filepath.Join(root, "custom-directory-skill")

	docsDir := filepath.Join(skillDir, "docs")
	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "readme.md"), []byte("docs content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "ref.md"), []byte("ref content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set custom directories — only "docs" should be scanned
	p := newProviderWithConfig(t, &fsskills.SourceOptions{
		ResourceDirectories: []string{"docs"},
	}, nil, root)

	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	// docs/readme.md should be readable
	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"custom-directory-skill","resourceName":"docs/readme.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "docs content" {
		t.Errorf("expected docs content, got %#v", result)
	}

	// references/ref.md should NOT be readable (defaults replaced)
	result, err = readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"custom-directory-skill","resourceName":"references/ref.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.HasPrefix(resultStr, "Error:") {
		t.Fatal("expected error for resource in replaced default directory")
	}
}

func TestDiscovery_ResourceExtensionFiltering(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "ext-skill", "Extension test", "Body.")
	skillDir := filepath.Join(root, "ext-skill")

	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "image.png"), []byte("fake image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	// .json is allowed by default
	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"ext-skill","resourceName":"references/data.json"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "{}" {
		t.Errorf("expected {}, got %#v", result)
	}

	// .png is NOT allowed by default
	result, err = readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"ext-skill","resourceName":"references/image.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.HasPrefix(resultStr, "Error:") {
		t.Fatal("expected error for .png file not in default extensions")
	}
}

func TestDiscovery_NestedResourceFiles_NotDiscovered(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "nested-res-skill", "Nested resources", "Body.")
	skillDir := filepath.Join(root, "nested-res-skill")

	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "top.md"), []byte("top content"), 0o644); err != nil {
		t.Fatal(err)
	}
	deepDir := filepath.Join(refsDir, "level1", "level2")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deepDir, "deep.md"), []byte("deep content"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProvider(t, root)
	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	// Top-level file in references/ should be found
	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"nested-res-skill","resourceName":"references/top.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "top content" {
		t.Errorf("expected top content, got %#v", result)
	}

	// Nested files are not discovered; only direct files in the configured directory are scanned.
	result, err = readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"nested-res-skill","resourceName":"references/level1/level2/deep.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.HasPrefix(resultStr, "Error:") {
		t.Fatal("expected error for nested file not directly inside the configured resource directory")
	}
}

func TestDiscovery_ResourceDirectoriesWithNestedPath(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "nested-directory-skill", "Nested directory", "Body.")
	skillDir := filepath.Join(root, "nested-directory-skill")

	nestedDir := filepath.Join(skillDir, "f1", "f2", "f3")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProviderWithConfig(t, &fsskills.SourceOptions{
		ResourceDirectories: []string{"f1/f2/f3"},
	}, nil, root)

	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"nested-directory-skill","resourceName":"f1/f2/f3/data.json"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "{}" {
		t.Errorf("expected {}, got %#v", result)
	}
}

func TestConfig_InvalidDirectoryName_Skipped(t *testing.T) {
	tests := []string{"..", "../escape", "sub/../escape", "/absolute"}
	for _, bad := range tests {
		t.Run(bad, func(t *testing.T) {
			// Invalid directories are skipped with a warning, not a panic
			p := newProviderWithConfig(t, &fsskills.SourceOptions{
				ResourceDirectories: []string{bad},
			}, nil, t.TempDir())
			if p == nil {
				t.Fatal("expected non-nil provider")
			}
		})
	}
}

func TestConfig_EmptyDirectoryName_Panics(t *testing.T) {
	tests := []struct {
		name string
		dir  string
	}{
		{"empty", ""},
		{"spaces", "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic for empty/whitespace directory name")
				}
			}()
			newProviderWithConfig(t, &fsskills.SourceOptions{
				ResourceDirectories: []string{tt.dir},
			}, nil, t.TempDir())
		})
	}
}

func TestConfig_ValidDirectoryNames(t *testing.T) {
	tests := []string{"scripts", "my-scripts", "sub/directory", ".", "./scripts", "./scripts/f1", "my..scripts"}
	for _, valid := range tests {
		t.Run(valid, func(t *testing.T) {
			p := newProviderWithConfig(t, &fsskills.SourceOptions{
				ResourceDirectories: []string{valid},
			}, nil, t.TempDir())
			if p == nil {
				t.Fatal("expected non-nil provider")
			}
		})
	}
}

func TestDiscovery_DuplicateDirectoriesAfterNormalization_NoDuplicateResources(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "dedup-directory-skill", "Dedup test", "Body.")
	skillDir := filepath.Join(root, "dedup-directory-skill")

	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "faq.md"), []byte("FAQ content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// "references" and "./references" should be deduplicated
	p := newProviderWithConfig(t, &fsskills.SourceOptions{
		ResourceDirectories: []string{"references", "./references"},
	}, nil, root)

	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"dedup-directory-skill","resourceName":"references/faq.md"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "FAQ content" {
		t.Errorf("expected FAQ content, got %#v", result)
	}
}

func TestDiscovery_CustomResourceExtensions(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "custom-ext-skill", "Custom extensions", "Body.")
	skillDir := filepath.Join(root, "custom-ext-skill")

	refsDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "data.custom"), []byte("custom data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "data.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newProviderWithConfig(t, &fsskills.SourceOptions{
		AllowedResourceExtensions: []string{".custom"},
	}, nil, root)

	_, tools := captureProviderContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	// .custom should be discoverable
	result, err := readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"custom-ext-skill","resourceName":"references/data.custom"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "custom data" {
		t.Errorf("expected custom data, got %#v", result)
	}

	// .json is NOT in custom extensions
	result, err = readTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"custom-ext-skill","resourceName":"references/data.json"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.HasPrefix(resultStr, "Error:") {
		t.Fatal("expected error for .json — not in custom extensions")
	}
}
