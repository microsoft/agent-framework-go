// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"context"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/agent/middleware/skills"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

// --- helpers ---

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

func newMiddleware(t *testing.T, roots ...string) middleware.Middleware {
	t.Helper()
	fsList := make([]fs.FS, len(roots))
	for i, r := range roots {
		fsList[i] = os.DirFS(r)
	}
	return skills.New(nil, fsList...)
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

func captureRunContext(t *testing.T, mw middleware.Middleware) (string, []tool.Tool) {
	t.Helper()

	var capturedMessages []*message.Message
	var capturedTools []tool.Tool

	next := func(_ context.Context, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = messages
		for tl := range agentopt.All(opts, agentopt.Tool) {
			capturedTools = append(capturedTools, tl)
		}
		return func(yield func(*message.ResponseUpdate, error) bool) {}
	}

	for _, err := range mw.Run(next, t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(capturedMessages) == 0 {
		return "", capturedTools
	}

	for _, content := range capturedMessages[0].Contents {
		if txt, ok := content.(*message.TextContent); ok {
			return txt.Text, capturedTools
		}
	}

	return "", capturedTools
}

// hasSkill checks that the provider discovers the given skill name
// (its name appears in the injected middleware instructions).
func hasSkill(t *testing.T, mw middleware.Middleware, name string) bool {
	t.Helper()
	instructions, _ := captureRunContext(t, mw)
	return strings.Contains(instructions, name)
}

// --- discovery and parsing tests ---

func TestDiscovery_ValidSkill(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "my-skill", "A test skill", "Use this skill to do things.")

	p := newMiddleware(t, root)
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

	p := newMiddleware(t, root)
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

	p := newMiddleware(t, root)
	instructions, tools := captureRunContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected no skills when frontmatter is missing")
	}
}

func TestDiscovery_MissingNameField_Excluded(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "no-name", "---\ndescription: A skill without a name\n---\nBody.")

	p := newMiddleware(t, root)
	instructions, tools := captureRunContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected no skills when name is missing")
	}
}

func TestDiscovery_MissingDescriptionField_Excluded(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "no-desc", "---\nname: no-desc\n---\nBody.")

	p := newMiddleware(t, root)
	instructions, tools := captureRunContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected no skills when description is missing")
	}
}

func TestDiscovery_InvalidName_Excluded(t *testing.T) {
	tests := []string{
		"BadName",
		"-leading-hyphen",
		"trailing-hyphen-",
		"has spaces",
	}
	for _, invalidName := range tests {
		t.Run(invalidName, func(t *testing.T) {
			root := t.TempDir()
			createSkillDirRaw(t, root, "invalid-name-test",
				"---\nname: "+invalidName+"\ndescription: A skill\n---\nBody.")

			p := newMiddleware(t, root)
			instructions, tools := captureRunContext(t, p)
			if instructions != "" || len(tools) != 0 {
				t.Fatalf("expected skill with name %q to be excluded", invalidName)
			}
		})
	}
}

func TestDiscovery_DuplicateNames_KeepsOne(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "skill-a", "---\nname: dupe\ndescription: First\n---\nFirst body.")
	createSkillDirRaw(t, root, "skill-b", "---\nname: dupe\ndescription: Second\n---\nSecond body.")

	p := newMiddleware(t, root)
	if !hasSkill(t, p, "dupe") {
		t.Fatal("expected one 'dupe' skill to be kept")
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

	p := newMiddleware(t, root)
	if hasSkill(t, p, "traversal-skill") {
		t.Fatal("expected traversal skill to be excluded")
	}
}

func TestDiscovery_NoFilesystems_ReturnsNil(t *testing.T) {
	p := skills.New(nil)
	instructions, tools := captureRunContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected nil context with no filesystems")
	}
}

func TestDiscovery_NestedSkillDirectory(t *testing.T) {
	root := t.TempDir()
	nestedDir := filepath.Join(root, "level1", "nested-skill")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "SKILL.md"),
		[]byte("---\nname: nested-skill\ndescription: Nested\n---\nNested body."), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newMiddleware(t, root)
	if !hasSkill(t, p, "nested-skill") {
		t.Fatal("expected nested skill to be discovered")
	}
}

func TestDiscovery_NameExceedsMaxLength_Excluded(t *testing.T) {
	root := t.TempDir()
	longName := strings.Repeat("a", 65)
	createSkillDirRaw(t, root, "long-name",
		"---\nname: "+longName+"\ndescription: A skill\n---\nBody.")

	p := newMiddleware(t, root)
	instructions, tools := captureRunContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected skill with long name to be excluded")
	}
}

func TestDiscovery_DescriptionExceedsMaxLength_Excluded(t *testing.T) {
	root := t.TempDir()
	longDesc := strings.Repeat("x", 1025)
	createSkillDirRaw(t, root, "long-desc",
		"---\nname: long-desc\ndescription: "+longDesc+"\n---\nBody.")

	p := newMiddleware(t, root)
	instructions, tools := captureRunContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected skill with long description to be excluded")
	}
}

func TestDiscovery_FileWithUtf8Bom(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "bom-skill",
		"\uFEFF---\nname: bom-skill\ndescription: Skill with BOM\n---\nBody content.")

	p := newMiddleware(t, root)
	if !hasSkill(t, p, "bom-skill") {
		t.Fatal("expected BOM skill to be discovered")
	}
}

func TestDiscovery_ResourceWithDotSlashPrefix_Loaded(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "dotslash-skill")
	refsDir := filepath.Join(skillDir, "refs")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "doc.md"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: dotslash-skill\ndescription: Dot-slash test\n---\nSee [doc](./refs/doc.md)."), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newMiddleware(t, root)
	if !hasSkill(t, p, "dotslash-skill") {
		t.Fatal("expected dotslash-skill to be discovered")
	}
}

// --- provider context tests ---

func TestProvider_NoSkills_ReturnsNil(t *testing.T) {
	root := t.TempDir()
	p := newMiddleware(t, root)

	instructions, tools := captureRunContext(t, p)
	if instructions != "" || len(tools) != 0 {
		t.Fatal("expected nil context when no skills")
	}
}

func TestProvider_WithSkills_ReturnsInstructionsAndTools(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "provider-skill", "Provider skill test", "Skill instructions body.")

	p := newMiddleware(t, root)
	_, tools := captureRunContext(t, p)
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

	p := skills.New(&skills.Config{
		SkillsInstructionPrompt: "Custom template: %s",
	}, os.DirFS(root))

	instructions, _ := captureRunContext(t, p)
	if !strings.HasPrefix(instructions, "Custom template:") {
		t.Errorf("expected custom template prefix, got %q", instructions)
	}
}

func TestProvider_SkillNamesAreXmlEscaped(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "xml-skill",
		"---\nname: xml-skill\ndescription: Uses <tags> & \"quotes\"\n---\nBody.")

	p := newMiddleware(t, root)
	instructions, _ := captureRunContext(t, p)
	if !strings.Contains(instructions, "&lt;tags&gt;") {
		t.Error("expected XML-escaped angle brackets")
	}
	if !strings.Contains(instructions, "&amp;") {
		t.Error("expected XML-escaped ampersand")
	}
}

func TestProvider_MultiplePaths(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root+"/dir1", "skill-a", "Skill A", "Body A.")
	createSkillDir(t, root+"/dir2", "skill-b", "Skill B", "Body B.")

	p := newMiddleware(t, root+"/dir1", root+"/dir2")
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

	p := newMiddleware(t, root)
	instructions, _ := captureRunContext(t, p)

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

func TestProvider_Run_NoError(t *testing.T) {
	root := t.TempDir()
	p := newMiddleware(t, root)

	next := func(_ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {}
	}

	for _, err := range p.Run(next, t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestProvider_Run_AutocallsSkillTools(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "skill-a", "A skill",
		"See [doc](refs/doc.md).", "refs/doc.md", "Doc content")

	p := newMiddleware(t, root)
	callCount := 0

	next := func(_ context.Context, messages []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		callCount++
		if callCount == 2 {
			if len(messages) == 0 {
				t.Fatal("expected messages on second invocation")
			}

			lastMsg := messages[len(messages)-1]
			if lastMsg.Role != message.RoleTool {
				t.Fatalf("expected last message role tool on second invocation, got %q", lastMsg.Role)
			}
			if len(lastMsg.Contents) != 2 {
				t.Fatalf("expected 2 function result contents on second invocation, got %d", len(lastMsg.Contents))
			}

			frc1, ok := lastMsg.Contents[0].(*message.FunctionResultContent)
			if !ok {
				t.Fatalf("expected first tool content to be FunctionResultContent, got %T", lastMsg.Contents[0])
			}
			if frc1.CallID != "call-load" || frc1.Result != "See [doc](refs/doc.md)." {
				t.Fatalf("unexpected first tool content: %+v", frc1)
			}

			frc2, ok := lastMsg.Contents[1].(*message.FunctionResultContent)
			if !ok {
				t.Fatalf("expected second tool content to be FunctionResultContent, got %T", lastMsg.Contents[1])
			}
			if frc2.CallID != "call-read" || frc2.Result != "Doc content" {
				t.Fatalf("unexpected second tool content: %+v", frc2)
			}

			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(&message.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.FunctionCallContent{
							CallID:    "call-load-2",
							Name:      "load_skill",
							Arguments: `{"skillName":"skill-a"}`,
						},
					},
				}, nil)
			}
		}

		if callCount == 3 {
			if len(messages) == 0 {
				t.Fatal("expected messages on third invocation")
			}

			lastMsg := messages[len(messages)-1]
			if lastMsg.Role != message.RoleTool {
				t.Fatalf("expected last message role tool on third invocation, got %q", lastMsg.Role)
			}
			if len(lastMsg.Contents) != 1 {
				t.Fatalf("expected 1 function result content on third invocation, got %d", len(lastMsg.Contents))
			}

			frc, ok := lastMsg.Contents[0].(*message.FunctionResultContent)
			if !ok {
				t.Fatalf("expected tool content to be FunctionResultContent, got %T", lastMsg.Contents[0])
			}
			if frc.CallID != "call-load-2" || frc.Result != "See [doc](refs/doc.md)." {
				t.Fatalf("unexpected third-invocation tool content: %+v", frc)
			}

			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(&message.ResponseUpdate{
					Role: message.RoleAssistant,
					Contents: []message.Content{
						&message.TextContent{Text: "Final answer"},
					},
				}, nil)
			}
		}

		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{
						CallID:    "call-load",
						Name:      "load_skill",
						Arguments: `{"skillName":"skill-a"}`,
					},
					&message.FunctionCallContent{
						CallID:    "call-read",
						Name:      "read_skill_resource",
						Arguments: `{"skillName":"skill-a","resourceName":"refs/doc.md"}`,
					},
				},
			}, nil)
		}
	}

	var updates []*message.ResponseUpdate
	for update, err := range p.Run(next, t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
	}

	if callCount != 3 {
		t.Fatalf("expected next to be called three times, got %d", callCount)
	}

	if len(updates) != 5 {
		t.Fatalf("expected 5 response updates, got %d", len(updates))
	}
	toolUpdate := updates[1]
	if toolUpdate.Role != message.RoleTool {
		t.Fatalf("expected tool role, got %q", toolUpdate.Role)
	}
	if len(toolUpdate.Contents) != 2 {
		t.Fatalf("expected 2 function result contents, got %d", len(toolUpdate.Contents))
	}

	frc1, ok := toolUpdate.Contents[0].(*message.FunctionResultContent)
	if !ok {
		t.Fatalf("expected first content to be FunctionResultContent, got %T", toolUpdate.Contents[0])
	}
	if frc1.CallID != "call-load" {
		t.Fatalf("expected call-load, got %q", frc1.CallID)
	}
	if frc1.Result != "See [doc](refs/doc.md)." {
		t.Fatalf("unexpected load_skill result: %v", frc1.Result)
	}

	frc2, ok := toolUpdate.Contents[1].(*message.FunctionResultContent)
	if !ok {
		t.Fatalf("expected second content to be FunctionResultContent, got %T", toolUpdate.Contents[1])
	}
	if frc2.CallID != "call-read" {
		t.Fatalf("expected call-read, got %q", frc2.CallID)
	}
	if frc2.Result != "Doc content" {
		t.Fatalf("unexpected read_skill_resource result: %v", frc2.Result)
	}

	secondAssistantUpdate := updates[2]
	if secondAssistantUpdate.Role != message.RoleAssistant {
		t.Fatalf("expected assistant role for second assistant update, got %q", secondAssistantUpdate.Role)
	}
	if len(secondAssistantUpdate.Contents) != 1 {
		t.Fatalf("expected one content in second assistant update, got %d", len(secondAssistantUpdate.Contents))
	}
	secondCall, ok := secondAssistantUpdate.Contents[0].(*message.FunctionCallContent)
	if !ok {
		t.Fatalf("expected second assistant content to be FunctionCallContent, got %T", secondAssistantUpdate.Contents[0])
	}
	if secondCall.CallID != "call-load-2" {
		t.Fatalf("expected second assistant call id call-load-2, got %q", secondCall.CallID)
	}

	secondToolUpdate := updates[3]
	if secondToolUpdate.Role != message.RoleTool {
		t.Fatalf("expected tool role for second tool update, got %q", secondToolUpdate.Role)
	}
	if len(secondToolUpdate.Contents) != 1 {
		t.Fatalf("expected one content in second tool update, got %d", len(secondToolUpdate.Contents))
	}
	thirdFrc, ok := secondToolUpdate.Contents[0].(*message.FunctionResultContent)
	if !ok {
		t.Fatalf("expected second tool content to be FunctionResultContent, got %T", secondToolUpdate.Contents[0])
	}
	if thirdFrc.CallID != "call-load-2" {
		t.Fatalf("expected call-load-2 in second tool update, got %q", thirdFrc.CallID)
	}
	if thirdFrc.Result != "See [doc](refs/doc.md)." {
		t.Fatalf("unexpected second tool update result: %v", thirdFrc.Result)
	}

	finalUpdate := updates[4]
	if finalUpdate.Role != message.RoleAssistant {
		t.Fatalf("expected assistant role for final update, got %q", finalUpdate.Role)
	}
	if len(finalUpdate.Contents) != 1 {
		t.Fatalf("expected one content in final update, got %d", len(finalUpdate.Contents))
	}
	txt, ok := finalUpdate.Contents[0].(*message.TextContent)
	if !ok {
		t.Fatalf("expected final content to be TextContent, got %T", finalUpdate.Contents[0])
	}
	if txt.Text != "Final answer" {
		t.Fatalf("unexpected final answer content: %q", txt.Text)
	}
}

func TestProvider_Run_SkipsCallsWithExistingResults(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "skill-a", "A skill", "Skill body")

	p := newMiddleware(t, root)

	next := func(_ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			if !yield(&message.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.FunctionCallContent{
						CallID:    "call-load",
						Name:      "load_skill",
						Arguments: `{"skillName":"skill-a"}`,
					},
				},
			}, nil) {
				return
			}
			yield(&message.ResponseUpdate{
				Role: message.RoleTool,
				Contents: []message.Content{
					&message.FunctionResultContent{CallID: "call-load", Result: "already-handled"},
				},
			}, nil)
		}
	}

	var updates []*message.ResponseUpdate
	for update, err := range p.Run(next, t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 2 {
		t.Fatalf("expected no additional response update, got %d", len(updates))
	}
}

// --- load_skill tool tests ---

func TestLoadSkill_ReturnsBody(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "load-test", "A skill", "Full instructions here.")

	p := newMiddleware(t, root)
	_, tools := captureRunContext(t, p)
	loadTool := findTool(t, tools, "load_skill")

	result, err := loadTool.Call(t.Context(), `{"skillName":"load-test"}`)
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

	p := newMiddleware(t, root)
	_, tools := captureRunContext(t, p)
	loadTool := findTool(t, tools, "load_skill")

	_, err := loadTool.Call(t.Context(), `{"skillName":"nonexistent"}`)
	if err == nil || !strings.HasPrefix(err.Error(), "Error:") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestLoadSkill_EmptyName(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "some-skill", "A skill", "Body.")

	p := newMiddleware(t, root)
	_, tools := captureRunContext(t, p)
	loadTool := findTool(t, tools, "load_skill")

	_, err := loadTool.Call(t.Context(), `{"skillName":""}`)
	if err == nil || !strings.HasPrefix(err.Error(), "Error:") {
		t.Fatalf("expected error, got %v", err)
	}
}

// --- read_skill_resource tool tests ---

func TestReadResource_ReturnsContent(t *testing.T) {
	root := t.TempDir()
	createSkillDirWithResource(t, root, "res-test", "A skill",
		"See [doc](refs/doc.md).", "refs/doc.md", "Resource content here.")

	p := newMiddleware(t, root)
	_, tools := captureRunContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(t.Context(), `{"skillName":"res-test","resourceName":"refs/doc.md"}`)
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

	p := newMiddleware(t, root)
	_, tools := captureRunContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	result, err := readTool.Call(t.Context(), `{"skillName":"dotslash-read","resourceName":"./refs/doc.md"}`)
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

	p := newMiddleware(t, root)
	_, tools := captureRunContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	_, err := readTool.Call(t.Context(), `{"skillName":"simple-skill","resourceName":"unknown.md"}`)
	if err == nil || !strings.HasPrefix(err.Error(), "Error:") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestReadResource_SkillNotFound(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "some-skill", "A skill", "Body.")

	p := newMiddleware(t, root)
	_, tools := captureRunContext(t, p)
	readTool := findTool(t, tools, "read_skill_resource")

	_, err := readTool.Call(t.Context(), `{"skillName":"nonexistent","resourceName":"file.md"}`)
	if err == nil || !strings.HasPrefix(err.Error(), "Error:") {
		t.Fatalf("expected error, got %v", err)
	}
}
