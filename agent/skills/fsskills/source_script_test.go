// Copyright (c) Microsoft. All rights reserved.

package fsskills_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

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

func TestFileSource_ScriptsOutsideScriptsDir_AreNotDiscovered(t *testing.T) {
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
	if len(loaded[0].Scripts) != 0 {
		t.Fatalf("expected no scripts, got %d", len(loaded[0].Scripts))
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

func TestFileSource_ScriptDirectoriesWithNestedPath_DiscoversScripts(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "nested-script-skill", "Nested script directory", "Body.")
	createRelativeFile(t, filepath.Join(root, "nested-script-skill"), "f1/f2/f3/run.py", "print('nested')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptDirectories: []string{"f1/f2/f3"},
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

func TestFileSource_ScriptDirectoryWithDotSlashPrefix_DiscoversScripts(t *testing.T) {
	directories := []string{"./scripts", "./scripts/f1", "./f2"}
	root := t.TempDir()
	createSkillDir(t, root, "dotslash-script-skill", "Dot-slash prefix", "Body.")
	skillDir := filepath.Join(root, "dotslash-script-skill")
	for _, directory := range directories {
		createRelativeFile(t, skillDir, directory[2:]+"/run.py", "print('dotslash')")
	}
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptDirectories: directories,
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return nil, nil
		},
	}, os.DirFS(root))

	loaded, err := source.Skills(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded[0].Scripts) != len(directories) {
		t.Fatalf("expected %d scripts, got %d", len(directories), len(loaded[0].Scripts))
	}
	for _, directory := range directories {
		expected := directory[2:] + "/run.py"
		found := false
		for _, script := range loaded[0].Scripts {
			if script.Name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected script %q to be discovered", expected)
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

func TestFileSource_ScriptInSkillRoot_DiscoveredWhenRootDirectoryConfigured(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "root-script-skill", "Root script", "Body.")
	createRelativeFile(t, filepath.Join(root, "root-script-skill"), "run.py", "print('hello')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptDirectories: []string{"."},
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

func TestFileSource_BackslashDirectoryNormalized_NoDuplicateScripts(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "backslash-skill", "Backslash test", "Body.")
	createRelativeFile(t, filepath.Join(root, "backslash-skill"), "scripts/run.py", "print('hello')")
	source := fsskills.NewSourceOptions(fsskills.SourceOptions{
		ScriptDirectories: []string{"scripts", ".\\scripts"},
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
		Content:     "Instructions.",
	}
	_, err = loaded[0].Scripts[0].Run(t.Context(), nonFileSkill, nil)
	if err == nil {
		t.Fatal("expected file script to reject non-file skill owner")
	}
}
