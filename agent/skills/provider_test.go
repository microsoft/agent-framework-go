// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

func funcToolPointer(t *testing.T, value tool.FuncTool) uintptr {
	t.Helper()
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Pointer {
		t.Fatalf("expected pointer-backed tool, got %T", value)
	}
	return rv.Pointer()
}

type countingSource struct {
	count  int
	skills []*skills.Skill
}

func (s *countingSource) Skills(context.Context) ([]*skills.Skill, error) {
	s.count++
	return s.skills, nil
}

type panicOnceSource struct {
	mu       sync.Mutex
	panicked bool
	skill    *skills.Skill
}

func (s *panicOnceSource) Skills(context.Context) ([]*skills.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.panicked {
		s.panicked = true
		panic("boom")
	}
	return []*skills.Skill{s.skill}, nil
}

func TestProvider_CustomPromptTemplate_MissingSkillsPlaceholderPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for template missing {skills}")
		}
	}()

	skill := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Instructions.", nil, nil)
	_ = skills.NewContextProvider(skills.ContextProviderOptions{SkillsInstructionPrompt: "No skills placeholder here {resource_instructions} {script_instructions}", Skills: []*skills.Skill{skill}})
}

func TestProvider_CustomPromptTemplate_MissingScriptInstructionsPlaceholderPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for template missing {script_instructions}")
		}
	}()

	skill := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Instructions.", nil, nil)
	_ = skills.NewContextProvider(skills.ContextProviderOptions{SkillsInstructionPrompt: "Has skills {skills} but no runner instructions {resource_instructions}", Skills: []*skills.Skill{skill}})
}

func TestProvider_CustomPromptTemplate_MissingResourceInstructionsPlaceholderPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for template missing {resource_instructions}")
		}
	}()

	skill := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Instructions.", nil, nil)
	_ = skills.NewContextProvider(skills.ContextProviderOptions{SkillsInstructionPrompt: "Has skills {skills} and runner {script_instructions} but no resource instructions", Skills: []*skills.Skill{skill}})
}

func providerFromFileSource(source *fsskills.Source, opts *skills.ContextProviderOptions) *agent.ContextProvider {
	if opts == nil {
		return skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
	}
	resolved := *opts
	resolved.Sources = []skills.Source{source}
	return skills.NewContextProvider(resolved)
}

func TestProvider_FromFileSourceWithOptions_DiscoversSkills(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "opts-skill", "Options skill", "Options body.")
	provider := providerFromFileSource(
		fsskills.NewSourceOptions(fsskills.SourceOptions{
			ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
				return nil, nil
			},
		}, os.DirFS(root)),
		&skills.ContextProviderOptions{DisableCaching: true},
	)

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "opts-skill") {
		t.Fatalf("expected opts-skill in instructions, got %q", instructions)
	}
}

func TestProvider_FromFileSourceWithMultipleFileSystems_DiscoversMultipleSkills(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, filepath.Join(root, "multi-opts-1"), "skill-x", "Skill X", "Body X.")
	createSkillDir(t, filepath.Join(root, "multi-opts-2"), "skill-y", "Skill Y", "Body Y.")
	provider := providerFromFileSource(
		fsskills.NewSourceOptions(fsskills.SourceOptions{
			ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
				return nil, nil
			},
		}, os.DirFS(filepath.Join(root, "multi-opts-1")), os.DirFS(filepath.Join(root, "multi-opts-2"))),
		&skills.ContextProviderOptions{DisableCaching: true},
	)

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "skill-x") || !strings.Contains(instructions, "skill-y") {
		t.Fatalf("expected both skills in instructions, got %q", instructions)
	}
}

func TestProvider_WithScriptsNoScriptApproval_DoesNotWrapRunScriptTool(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "no-approval-skill", "No approval test", "Body.")
	createRelativeFile(t, filepath.Join(root, "no-approval-skill"), "scripts/run.py", "print('hello')")
	provider := newProviderWithConfig(t, &fsskills.SourceOptions{ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
		return "ok", nil
	}}, nil, root)

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, "run_skill_script")
	if _, ok := runTool.(tool.ApprovalRequiredTool); ok {
		t.Fatal("did not expect run_skill_script to require approval by default")
	}
}

func TestProvider_MultipleInvocations_ToolsAreSharedWhenCached(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "cached-tools-skill", "Cached tools test", "Body.")
	provider := newProvider(t, root)

	_, tools1 := captureProviderContext(t, provider)
	_, tools2 := captureProviderContext(t, provider)
	ptr1 := funcToolPointer(t, findTool(t, tools1, "load_skill"))
	ptr2 := funcToolPointer(t, findTool(t, tools2, "load_skill"))
	if ptr1 != ptr2 {
		t.Fatal("expected cached provider to reuse the same tool instance")
	}
}

func TestProvider_MultipleInvocations_ToolsAreNotSharedWhenCachingDisabled(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "fresh-tools-skill", "Fresh tools test", "Body.")
	provider := newProviderWithConfig(t, nil, &skills.ContextProviderOptions{DisableCaching: true}, root)

	_, tools1 := captureProviderContext(t, provider)
	_, tools2 := captureProviderContext(t, provider)
	ptr1 := funcToolPointer(t, findTool(t, tools1, "load_skill"))
	ptr2 := funcToolPointer(t, findTool(t, tools2, "load_skill"))
	if ptr1 == ptr2 {
		t.Fatal("expected uncached provider to rebuild tools on each invocation")
	}
}

func TestNew_MultipleDirectories_DeduplicatesSkillsByName(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, filepath.Join(root, "dup1"), "dup-skill", "First", "Body 1.")
	createSkillDir(t, filepath.Join(root, "dup2"), "dup-skill", "Second", "Body 2.")
	provider := newProvider(t, filepath.Join(root, "dup1"), filepath.Join(root, "dup2"))

	_, tools := captureProviderContext(t, provider)
	loadTool := findTool(t, tools, "load_skill")
	content, err := loadTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"dup-skill"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.(string), "Body 1.") {
		t.Fatalf("expected first duplicate to survive, got %q", content)
	}
}

func TestNewProvider_DeduplicatesSkillsByName(t *testing.T) {
	first := mustInlineSkill(skills.Frontmatter{Name: "dup-inline", Description: "First"}, "First instructions.", nil, nil)
	second := mustInlineSkill(skills.Frontmatter{Name: "dup-inline", Description: "Second"}, "Second instructions.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{first, second}})

	_, tools := captureProviderContext(t, provider)
	loadTool := findTool(t, tools, "load_skill")
	content, err := loadTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"dup-inline"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.(string), "First instructions.") {
		t.Fatalf("expected first duplicate to survive, got %q", content)
	}
}

func TestProvider_DefaultCaching_LoadsSourceOnce(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "cached-skill", Description: "Cached skill"},
		"Instructions.",
		nil,
		nil,
	)
	source := &countingSource{skills: []*skills.Skill{skill}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	_, _ = captureProviderContext(t, provider)
	_, _ = captureProviderContext(t, provider)

	if source.count != 1 {
		t.Fatalf("expected source to be loaded once, got %d", source.count)
	}
}

func TestProvider_DisableCaching_LoadsSourceEachTime(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "fresh-skill", Description: "Fresh skill"},
		"Instructions.",
		nil,
		nil,
	)
	source := &countingSource{skills: []*skills.Skill{skill}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}, DisableCaching: true})

	_, _ = captureProviderContext(t, provider)
	_, _ = captureProviderContext(t, provider)

	if source.count < 2 {
		t.Fatalf("expected source to be loaded at least twice, got %d", source.count)
	}
}

func TestProvider_RecoversFromPanickingSourceAndResetsLoading(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "recovered-skill", Description: "Recovered skill"},
		"Instructions.",
		nil,
		nil,
	)
	source := &panicOnceSource{skill: skill}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	_, _, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
	if err == nil {
		t.Fatal("expected provider to return an error after source panic")
	}
	if !strings.Contains(err.Error(), "building skills context panicked: boom") {
		t.Fatalf("expected recovered panic error, got %v", err)
	}

	type result struct {
		hasInstructions bool
		err             error
	}
	resultCh := make(chan result, 1)
	go func() {
		_, options, err := provider.BeforeRun(t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
		instructions, _ := agent.GetOption(options, agent.WithInstructions)
		resultCh <- result{hasInstructions: strings.Contains(instructions, "recovered-skill"), err: err}
	}()

	select {
	case outcome := <-resultCh:
		if outcome.err != nil {
			t.Fatalf("expected second provider call to succeed, got %v", outcome.err)
		}
		if !outcome.hasInstructions {
			t.Fatal("expected provider to recover and return instructions on the second call")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provider to recover after panic")
	}
}

func TestProvider_SkipsNilSkillsReturnedBySource(t *testing.T) {
	validSkill := mustInlineSkill(
		skills.Frontmatter{Name: "valid-skill", Description: "Valid skill"},
		"Instructions.",
		nil,
		nil,
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{&countingSource{skills: []*skills.Skill{nil, validSkill}}},
	})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "valid-skill") {
		t.Fatalf("expected valid skill to remain available, got %q", instructions)
	}
}

func TestProvider_SkipsSkillsWithInvalidFrontmatterReturnedBySource(t *testing.T) {
	validSkill := mustInlineSkill(
		skills.Frontmatter{Name: "valid-skill", Description: "Valid skill"},
		"Instructions.",
		nil,
		nil,
	)
	invalidSkill := &skills.Skill{
		Frontmatter: skills.Frontmatter{Name: "MyConverter", Description: "Invalid skill"},
		Content:     "Should be skipped.",
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{&countingSource{skills: []*skills.Skill{invalidSkill, validSkill}}},
	})

	instructions, _ := captureProviderContext(t, provider)
	if strings.Contains(instructions, "MyConverter") {
		t.Fatalf("expected invalid skill to be skipped, got %q", instructions)
	}
	if !strings.Contains(instructions, "valid-skill") {
		t.Fatalf("expected valid skill to remain available, got %q", instructions)
	}
	if strings.Contains(instructions, "Should be skipped.") {
		t.Fatalf("expected invalid skill content to be skipped, got %q", instructions)
	}
}

func TestNewContextProvider_WithInvalidInlineSkillPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for inline skill with invalid frontmatter")
		}
	}()

	_ = skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{{
			Frontmatter: skills.Frontmatter{Name: "MyConverter", Description: "Invalid skill"},
			Content:     "Instructions.",
		}},
	})
}

func TestProvider_WithScripts_ExposesRunSkillScriptTool(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "script-skill", "Script skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "script-skill"), "scripts/run.py", "print('ok')")

	runnerCalled := false
	provider := newProviderWithConfig(t, &fsskills.SourceOptions{
		ScriptRunner: func(_ context.Context, skill *skills.Skill, script *skills.Script, arguments []string) (any, error) {
			runnerCalled = true
			if skill.Frontmatter.Name != "script-skill" {
				t.Fatalf("expected script-skill, got %q", skill.Frontmatter.Name)
			}
			if script.Name != "scripts/run.py" {
				t.Fatalf("expected scripts/run.py, got %q", script.Name)
			}
			return map[string]any{"status": "ok", "args": arguments}, nil
		},
	}, nil, root)

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, "run_skill_script")
	result, err := runTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"script-skill","scriptName":"scripts/run.py","arguments":["--value","42"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if !runnerCalled {
		t.Fatal("expected script runner to be called")
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected structured script result, got %T", result)
	}
	args, ok := resultMap["args"].([]string)
	if !ok || resultMap["status"] != "ok" || len(args) != 2 || args[0] != "--value" || args[1] != "42" {
		t.Fatalf("expected structured script result with args, got %#v", resultMap)
	}
}

func TestProvider_RunSkillScript_RequiresExactName(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "script-skill", "Script skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "script-skill"), "scripts/run.py", "print('ok')")

	provider := newProviderWithConfig(t, &fsskills.SourceOptions{
		ScriptRunner: func(_ context.Context, _ *skills.Skill, _ *skills.Script, _ []string) (any, error) {
			t.Fatal("did not expect script runner to be called")
			return nil, nil
		},
	}, nil, root)

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, "run_skill_script")
	result, err := runTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"script-skill","scriptName":"./scripts/run.py"}`)
	if err != nil {
		t.Fatalf("expected no tool error, got %v", err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if resultStr != "Error: Script './scripts/run.py' not found in skill 'script-skill'." {
		t.Fatalf("expected exact-name error, got %q", resultStr)
	}
}

func TestProvider_ScriptApproval_MarksToolAsApprovalRequired(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "approval-skill", "Approval skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "approval-skill"), "scripts/run.py", "print('ok')")

	provider := newProviderWithConfig(t, &fsskills.SourceOptions{
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return "ok", nil
		},
	}, &skills.ContextProviderOptions{ScriptApproval: true}, root)

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, "run_skill_script")
	if _, ok := runTool.(tool.ApprovalRequiredTool); !ok {
		t.Fatal("expected run_skill_script to require approval")
	}
}

func TestProvider_FromFileSourceWithRunner_UsesScriptRunner(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "runner-skill", "Runner skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "runner-skill"), "scripts/run.py", "print('ok')")

	runnerCalled := false
	provider := providerFromFileSource(
		fsskills.NewSourceOptions(fsskills.SourceOptions{
			ScriptRunner: func(_ context.Context, _ *skills.Skill, _ *skills.Script, _ []string) (any, error) {
				runnerCalled = true
				return "executed", nil
			},
		}, os.DirFS(root)),
		nil,
	)

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, "run_skill_script")
	result, err := runTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"runner-skill","scriptName":"scripts/run.py"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "executed" {
		t.Fatalf("expected executed, got %#v", result)
	}
	if !runnerCalled {
		t.Fatal("expected file-source script runner to be used")
	}
}

func TestNewProvider_ProvidesInlineSkills(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"},
		"Inline instructions.",
		nil,
		nil,
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})
	instructions, tools := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "inline-skill") {
		t.Fatal("expected inline skill to be advertised")
	}
	if len(tools) != 1 || tools[0].Name() != "load_skill" {
		t.Fatal("expected only load_skill tool for inline skills without resources or scripts")
	}
}

func TestNewProvider_ProvideExtendsInput(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"},
		"Inline instructions.",
		nil,
		nil,
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})
	session := agenttest.CreateSession()
	input := message.NewText("input")

	messages, options, err := provider.Provide(t.Context(), []*message.Message{input}, agent.WithSession(session))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected only input message, got %d", len(messages))
	}
	if messages[0] != input {
		t.Fatal("expected original input message to be preserved first")
	}
	if instructions, ok := agent.GetOption(options, agent.WithInstructions); !ok || !strings.Contains(instructions, "inline-skill") {
		t.Fatalf("expected skills instructions option, got %q", instructions)
	}
	if gotSession, ok := agent.GetOption(options, agent.WithSession); !ok || gotSession != session {
		t.Fatal("expected original session option to be preserved")
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	if len(tools) != 1 || tools[0].Name() != "load_skill" {
		t.Fatal("expected skills provider to append load_skill tool")
	}
}

func TestProvider_WithEmptySource_ReturnsEmptyProvider(t *testing.T) {
	provider := skills.NewContextProvider(skills.ContextProviderOptions{})
	ctx := context.Background()
	messages, options, err := provider.BeforeRun(ctx, nil, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatal(err)
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	if len(messages) != 0 || len(tools) != 0 {
		t.Fatal("expected empty provider context when no sources are configured")
	}
}

func TestProvider_AggregatesMultipleSources(t *testing.T) {
	fileRoot := t.TempDir()
	createSkillDir(t, fileRoot, "file-skill", "File skill", "Body.")

	inline := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline"}, "Inline body.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{inline},
		Sources: []skills.Source{
			fsskills.NewSourceOptions(fsskills.SourceOptions{
				ScriptDirectories: []string{"unused-scripts"},
				ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
					return "ok", nil
				},
			}, os.DirFS(fileRoot)),
		},
	})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "inline-skill") {
		t.Fatal("expected inline-skill in aggregated instructions")
	}
	if !strings.Contains(instructions, "file-skill") {
		t.Fatal("expected file-skill in aggregated instructions")
	}
}

func TestProvider_SkillFilter_FiltersInlineAndSourceSkills(t *testing.T) {
	fileRoot := t.TempDir()
	createSkillDir(t, fileRoot, "keep-source", "Keep source", "Keep source body.")
	createSkillDir(t, fileRoot, "drop-source", "Drop source", "Drop source body.")

	keepInline := mustInlineSkill(skills.Frontmatter{Name: "keep-inline", Description: "Keep inline"}, "Keep inline body.", nil, nil)
	dropInline := mustInlineSkill(skills.Frontmatter{Name: "drop-inline", Description: "Drop inline"}, "Drop inline body.", nil, nil)

	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{keepInline, dropInline},
		Sources: []skills.Source{
			fsskills.NewSourceOptions(fsskills.SourceOptions{}, os.DirFS(fileRoot)),
		},
		SkillFilter: func(skill *skills.Skill) bool {
			return strings.HasPrefix(skill.Frontmatter.Name, "keep-")
		},
	})

	instructions, tools := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "keep-inline") || !strings.Contains(instructions, "keep-source") {
		t.Fatalf("expected kept skills in instructions, got %q", instructions)
	}
	if strings.Contains(instructions, "drop-inline") || strings.Contains(instructions, "drop-source") {
		t.Fatalf("expected filtered skills to be excluded, got %q", instructions)
	}

	loadTool := findTool(t, tools, "load_skill")
	result, err := loadTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"drop-inline"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Error: Skill 'drop-inline' not found." {
		t.Fatalf("expected filtered inline skill to be unavailable, got %#v", result)
	}

	result, err = loadTool.Call(tool.Context{Context: t.Context()}, `{"skillName":"drop-source"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Error: Skill 'drop-source' not found." {
		t.Fatalf("expected filtered source skill to be unavailable, got %#v", result)
	}
}

func TestProvider_SkillFilter_CanFilterOutAllSkills(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Inline instructions.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{skill},
		SkillFilter: func(*skills.Skill) bool {
			return false
		},
	})

	ctx := context.Background()
	messages, options, err := provider.BeforeRun(ctx, nil, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatal(err)
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	if len(messages) != 0 || len(tools) != 0 {
		t.Fatalf("expected empty provider context when filter removes all skills, got messages=%d tools=%d", len(messages), len(tools))
	}
}

func TestNewProvider_DisableSourceDeduplication_PreservesDuplicateSkillsInInstructions(t *testing.T) {
	first := mustInlineSkill(skills.Frontmatter{Name: "dup-inline", Description: "First"}, "First instructions.", nil, nil)
	second := mustInlineSkill(skills.Frontmatter{Name: "dup-inline", Description: "Second"}, "Second instructions.", nil, nil)

	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills:                     []*skills.Skill{first, second},
		DisableSourceDeduplication: true,
	})

	instructions, _ := captureProviderContext(t, provider)
	if strings.Count(instructions, "<name>dup-inline</name>") != 2 {
		t.Fatalf("expected duplicate skills to remain in instructions, got %q", instructions)
	}
}
