// Copyright (c) Microsoft. All rights reserved.

package fsskills_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
)

func TestFileSource_WithScriptFiles_DiscoversScripts(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "my-skill", "A test skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "my-skill"), "scripts/convert.py", "print('hello')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
		return nil, nil
	}}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scriptsFound := loaded[0].Scripts
	if len(scriptsFound) != 1 {
		t.Fatalf("expected 1 script, got %d", len(scriptsFound))
	}
	if scriptsFound[0].Name != "scripts/convert.py" {
		t.Fatalf("expected scripts/convert.py, got %q", scriptsFound[0].Name)
	}
}

func TestFileSource_WithMultipleScriptExtensions_DiscoversAll(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "multi-ext-skill", "Multi-extension skill", "Body.")
	skillDir := filepath.Join(root, "multi-ext-skill")
	for _, fileName := range []string{"scripts/run.py", "scripts/run.sh", "scripts/run.js", "scripts/run.ps1", "scripts/run.cs", "scripts/run.csx"} {
		createRelativeFile(t, skillDir, fileName, "echo")
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
		return nil, nil
	}}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scriptNames := make([]string, 0, len(loaded[0].Scripts))
	for _, script := range loaded[0].Scripts {
		scriptNames = append(scriptNames, script.Name)
	}
	sort.Strings(scriptNames)
	if len(scriptNames) != 6 {
		t.Fatalf("expected 6 scripts, got %d", len(scriptNames))
	}
	for _, expected := range []string{"scripts/run.cs", "scripts/run.csx", "scripts/run.js", "scripts/run.ps1", "scripts/run.py", "scripts/run.sh"} {
		if scriptNames[0] == "" {
			break
		}
		found := false
		for _, actual := range scriptNames {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected script %q to be discovered, got %#v", expected, scriptNames)
		}
	}
}

func TestFileSource_NonScriptExtensionsAreNotDiscovered(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "no-script-skill", "Non-script skill", "Body.")
	skillDir := filepath.Join(root, "no-script-skill")
	createRelativeFile(t, skillDir, "scripts/data.txt", "text data")
	createRelativeFile(t, skillDir, "scripts/config.json", "{}")
	createRelativeFile(t, skillDir, "scripts/notes.md", "# Notes")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
		return nil, nil
	}}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded[0].Scripts) != 0 {
		t.Fatalf("expected no scripts, got %d", len(loaded[0].Scripts))
	}
}

func TestFileSource_NoScriptFiles_ReturnsEmptyScripts(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "no-scripts", "No scripts skill", "Body.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded[0].Scripts) != 0 {
		t.Fatalf("expected no scripts, got %d", len(loaded[0].Scripts))
	}
}

func TestFileSource_ScriptsInAnyDirectory_AreDiscovered(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "root-scripts", "Root scripts skill", "Body.")
	skillDir := filepath.Join(root, "root-scripts")
	createRelativeFile(t, skillDir, "convert.py", "print('root')")
	createRelativeFile(t, skillDir, "tools/helper.sh", "echo 'helper'")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
		return nil, nil
	}}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// With depth-based scanning, scripts in any directory within search depth are discovered.
	if len(loaded[0].Scripts) != 2 {
		t.Fatalf("expected 2 scripts, got %d: %v", len(loaded[0].Scripts), loaded[0].Scripts)
	}
}

func TestFileSource_WithRunner_ScriptsCanRun(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "exec-skill", "Executor test", "Body.")
	createRelativeFile(t, filepath.Join(root, "exec-skill"), "scripts/test.py", "print('ok')")
	executorCalled := false
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: func(_ context.Context, skill *skills.Skill, script *skills.Script, arguments []string) (any, error) {
		executorCalled = true
		if skill.Frontmatter.Name != "exec-skill" {
			t.Fatalf("expected exec-skill, got %q", skill.Frontmatter.Name)
		}
		if script.Name != "scripts/test.py" {
			t.Fatalf("expected scripts/test.py, got %q", script.Name)
		}
		return "executed", nil
	}}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err := loaded[0].Scripts[0].Run(t.Context(), loaded[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !executorCalled {
		t.Fatal("expected executor to be called")
	}
	if result != "executed" {
		t.Fatalf("expected executed, got %q", result)
	}
}

func TestFileSource_NullRunner_DoesNotPanic(t *testing.T) {
	_ = fsskills.NewSource(os.DirFS(t.TempDir()))
}

func TestFileSource_ScriptsWithNoRunner_ReturnsErrorOnRun(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "no-runner-skill", "No runner", "Body.")
	createRelativeFile(t, filepath.Join(root, "no-runner-skill"), "scripts/run.sh", "echo 'hello'")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = loaded[0].Scripts[0].Run(t.Context(), loaded[0], nil)
	if err == nil {
		t.Fatal("expected script run to fail without a runner")
	}
}

func TestFileSource_CustomScriptExtensions_OnlyDiscoversMatching(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "custom-ext-skill", "Custom extensions", "Body.")
	skillDir := filepath.Join(root, "custom-ext-skill")
	createRelativeFile(t, skillDir, "scripts/run.py", "print('py')")
	createRelativeFile(t, skillDir, "scripts/run.rb", "puts 'rb'")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		AllowedScriptExtensions: []string{".rb"},
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scriptsFound := loaded[0].Scripts
	if len(scriptsFound) != 1 {
		t.Fatalf("expected 1 script, got %d", len(scriptsFound))
	}
	if scriptsFound[0].Name != "scripts/run.rb" {
		t.Fatalf("expected scripts/run.rb, got %q", scriptsFound[0].Name)
	}
}

func TestFileSource_ExecutorReceivesArguments(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "args-skill", "Args test", "Body.")
	createRelativeFile(t, filepath.Join(root, "args-skill"), "scripts/test.py", "print('ok')")
	var captured []string
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: func(_ context.Context, _ *skills.Skill, _ *skills.Script, arguments []string) (any, error) {
		captured = arguments
		return "done", nil
	}}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"--value", "26.2", "--factor", "1.60934"}
	if _, err := loaded[0].Scripts[0].Run(t.Context(), loaded[0], arguments); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 4 || captured[0] != "--value" || captured[1] != "26.2" || captured[2] != "--factor" || captured[3] != "1.60934" {
		t.Fatalf("unexpected captured arguments: %#v", captured)
	}
}

func TestFileSource_ScriptAtConfigurableDepth_DiscoversWithSearchDepth(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "nested-script-skill", "Nested script directory", "Body.")
	createRelativeFile(t, filepath.Join(root, "nested-script-skill"), "f1/f2/f3/run.py", "print('nested')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		SearchDepth: 4,
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scriptsFound := loaded[0].Scripts
	if len(scriptsFound) != 1 {
		t.Fatalf("expected 1 script, got %d", len(scriptsFound))
	}
	if scriptsFound[0].Name != "f1/f2/f3/run.py" {
		t.Fatalf("expected f1/f2/f3/run.py, got %q", scriptsFound[0].Name)
	}
}

func TestFileSource_ScriptsInMultipleSubdirectories_AllDiscovered(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "multidir-script-skill", "Multi-dir scripts", "Body.")
	skillDir := filepath.Join(root, "multidir-script-skill")
	// Create scripts at root and one subdirectory level (within default depth 2)
	createRelativeFile(t, skillDir, "scripts/run.py", "print('scripts')")
	createRelativeFile(t, skillDir, "f2/run.py", "print('f2')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"f2/run.py", "scripts/run.py"}
	found := make(map[string]bool)
	for _, s := range loaded[0].Scripts {
		found[s.Name] = true
	}
	for _, name := range expected {
		if !found[name] {
			t.Fatalf("expected script %q to be discovered; got %v", name, loaded[0].Scripts)
		}
	}
}

func TestFileSource_InvalidScriptExtension_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for invalid script extension")
		}
	}()

	_ = fsskills.NewSourceOptions(fsskills.SourceOptions{AllowedScriptExtensions: []string{"txt"}}, os.DirFS(t.TempDir()))
}

func TestFileSource_ScriptAtSkillRoot_Discovered(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "root-script-skill", "Root script", "Body.")
	createRelativeFile(t, filepath.Join(root, "root-script-skill"), "run.py", "print('hello')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scriptsFound := loaded[0].Scripts
	if len(scriptsFound) != 1 {
		t.Fatalf("expected 1 script, got %d", len(scriptsFound))
	}
	if scriptsFound[0].Name != "run.py" {
		t.Fatalf("expected run.py, got %q", scriptsFound[0].Name)
	}
}

func TestFileSource_ScriptFilter_IncludesOnlyMatchingScripts(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "backslash-skill", "Script filter test", "Body.")
	createRelativeFile(t, filepath.Join(root, "backslash-skill"), "scripts/run.py", "print('hello')")
	createRelativeFile(t, filepath.Join(root, "backslash-skill"), "scripts/skip.py", "print('skip')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptFilter: func(ctx fsskills.FilterContext) bool {
			return ctx.RelativeFilePath == "scripts/run.py"
		},
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded[0].Scripts) != 1 {
		t.Fatalf("expected 1 script, got %d", len(loaded[0].Scripts))
	}
	if loaded[0].Scripts[0].Name != "scripts/run.py" {
		t.Fatalf("expected scripts/run.py, got %q", loaded[0].Scripts[0].Name)
	}
}

func TestFileScript_RunWithNonFileSkill_ReturnsError(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "script-owner", "Script owner", "Body.")
	createRelativeFile(t, filepath.Join(root, "script-owner"), "scripts/run.py", "print('ok')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
		return "result", nil
	}}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	nonFileSkill := &skills.Skill{
		Frontmatter: skills.Frontmatter{Name: "my-skill", Description: "A skill"},
		GetContent: func(context.Context) (string, error) {
			return "Instructions.", nil
		},
	}
	_, err = loaded[0].Scripts[0].Run(t.Context(), nonFileSkill, nil)
	if err == nil {
		t.Fatal("expected file script to reject non-file skill owner")
	}
}

func TestFileScript_HasDefaultParametersSchema(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "schema-skill", "A test skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "schema-skill"), "scripts/convert.py", "print('hello')")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	script := loaded[0].Scripts[0]
	const want = `{"type":"array","items":{"type":"string"}}`
	if script.ParametersSchema != want {
		t.Fatalf("expected ParametersSchema %q, got %q", want, script.ParametersSchema)
	}
}

func TestFileSkill_WithScripts_ContentIncludesAvailableScriptsBlock(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "schema-content-skill", "A test skill", "Instructions here.")
	createRelativeFile(t, filepath.Join(root, "schema-content-skill"), "build.sh", "echo build")
	createRelativeFile(t, filepath.Join(root, "schema-content-skill"), "deploy.sh", "echo deploy")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := loaded[0].GetContent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "<available_scripts>") {
		t.Fatalf("expected <available_scripts> block in content, got: %s", content)
	}
	if !strings.Contains(content, `<script name="build.sh">`) {
		t.Fatalf("expected <script name=\"build.sh\"> in content, got: %s", content)
	}
	if !strings.Contains(content, `<script name="deploy.sh">`) {
		t.Fatalf("expected <script name=\"deploy.sh\"> in content, got: %s", content)
	}
	if !strings.Contains(content, "<parameters_schema>") {
		t.Fatalf("expected <parameters_schema> in content, got: %s", content)
	}
	if !strings.Contains(content, "</available_scripts>") {
		t.Fatalf("expected </available_scripts> in content, got: %s", content)
	}
}

func TestFileSkill_WithScripts_ContentStartsWithOriginalSkillMd(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "original-content-skill", "A test skill", "Original instructions.")
	createRelativeFile(t, filepath.Join(root, "original-content-skill"), "run.py", "print('run')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := loaded[0].GetContent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Original instructions.") {
		t.Fatalf("expected original SKILL.md content to be preserved, got: %s", content)
	}
	if !strings.Contains(content, "<available_resources />") {
		t.Fatalf("expected empty <available_resources /> block appended, got: %s", content)
	}
	if !strings.Contains(content, "<available_scripts>") {
		t.Fatalf("expected <available_scripts> block appended, got: %s", content)
	}
}

func TestFileSkill_WithoutScripts_ContentIncludesEmptyAvailableScriptsBlock(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "no-script-content-skill", "A test skill", "Instructions here.")
	source := fsskills.NewSource(os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := loaded[0].GetContent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "<available_scripts />") {
		t.Fatalf("expected empty <available_scripts /> block when skill has no scripts, got: %s", content)
	}
}

func TestFileSkill_ScriptContent_IncludesDefaultArraySchema(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "schema-inline-skill", "A test skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "schema-inline-skill"), "scripts/search.py", "print('search')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := loaded[0].GetContent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// The default schema {"type":"array","items":{"type":"string"}} should appear in the content.
	if !strings.Contains(content, `"type":"array"`) {
		t.Fatalf("expected default array schema in content, got: %s", content)
	}
	// Quotes in JSON schema should be preserved (not escaped as &quot;).
	if strings.Contains(content, "&quot;") {
		t.Fatalf("expected JSON quotes to be preserved in schema content, got: %s", content)
	}
}

// On a case-sensitive filesystem, two files in the same skill directory that
// differ only in case (e.g. Data.json and data.json) are distinct files and
// must both be discovered. Deduping on a lowercased path collapsed them.
func TestFileSource_CaseSensitiveFS_KeepsDistinctlyCasedFiles(t *testing.T) {
	// fstest.MapFS keys are case-sensitive regardless of the host filesystem.
	fsys := fstest.MapFS{
		"case-skill/SKILL.md":  {Data: []byte("---\nname: case-skill\ndescription: d\n---\nbody")},
		"case-skill/Data.json": {Data: []byte("A")},
		"case-skill/data.json": {Data: []byte("B")},
		"case-skill/Run.py":    {Data: []byte("A")},
		"case-skill/run.py":    {Data: []byte("B")},
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) { return nil, nil },
	}, fsys)

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if got := len(loaded[0].Resources); got != 2 {
		t.Errorf("expected 2 resources (Data.json + data.json) on a case-sensitive FS, got %d", got)
	}
	if got := len(loaded[0].Scripts); got != 2 {
		t.Errorf("expected 2 scripts (Run.py + run.py) on a case-sensitive FS, got %d", got)
	}
}
