// Copyright (c) Microsoft. All rights reserved.

package skills_test

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const (
	loadSkillToolName         = "load_skill"
	readSkillResourceToolName = "read_skill_resource"
	runSkillScriptToolName    = "run_skill_script"
)

func funcToolPointer(t *testing.T, value tool.FuncTool) uintptr {
	t.Helper()
	current := value
	for {
		rv := reflect.ValueOf(current)
		if rv.Kind() == reflect.Pointer {
			return rv.Pointer()
		}
		if rv.Kind() != reflect.Struct || rv.NumField() != 1 {
			t.Fatalf("expected pointer-backed tool, got %T", current)
		}
		embedded, ok := rv.Field(0).Interface().(tool.FuncTool)
		if !ok {
			t.Fatalf("expected pointer-backed tool, got %T", current)
		}
		current = embedded
	}
}

func assertToolApprovalRequired(t *testing.T, value tool.FuncTool, want bool) {
	t.Helper()
	approval, ok := value.(tool.ApprovalRequiredTool)
	if !ok {
		if want {
			t.Fatalf("expected %q to implement ApprovalRequiredTool", value.Name())
		}
		return
	}
	if got := approval.ApprovalRequired(); got != want {
		t.Fatalf("expected %q approval requirement to be %t, got %t", value.Name(), want, got)
	}
}

type countingSource struct {
	count  int
	skills []*skills.Skill
}

func invokeProvider(provider agent.ContextProvider, ctx context.Context, messages []*message.Message, options ...agent.Option) ([]*message.Message, []agent.Option, error) {
	return provider.Invoking(ctx, agent.InvokingContext{Messages: messages, Options: options})
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

type blockingSource struct {
	started     chan struct{}
	startedOnce sync.Once
	release     chan struct{}
	releaseOnce sync.Once
	skill       *skills.Skill
}

func (s *blockingSource) Skills(ctx context.Context) ([]*skills.Skill, error) {
	s.startedOnce.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return []*skills.Skill{s.skill}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *blockingSource) unblock() {
	s.releaseOnce.Do(func() { close(s.release) })
}

func TestProvider_CustomPromptTemplate_MissingSkillsPlaceholderPanics(t *testing.T) {
	defer func() {
		recovered := recover()
		err, ok := recovered.(error)
		if !ok || err.Error() != "custom prompt template must contain the \"{skills}\" placeholder" {
			t.Fatalf("expected missing {skills} placeholder error, got %v", recovered)
		}
	}()

	skill := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Instructions.", nil, nil)
	_ = skills.NewContextProvider(skills.ContextProviderOptions{SkillsInstructionPrompt: new("No skills placeholder here {resource_instructions} {script_instructions}"), Skills: []*skills.Skill{skill}})
}

func TestProvider_CustomPromptTemplate_MissingLegacyInstructionPlaceholdersAccepted(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Instructions.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		SkillsInstructionPrompt: new("Custom skills:\n{skills}"),
		Skills:                  []*skills.Skill{skill},
	})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "<name>inline-skill</name>") {
		t.Fatalf("expected generated skills list, got %q", instructions)
	}
	if strings.Contains(instructions, "read_skill_resource") || strings.Contains(instructions, "run_skill_script") {
		t.Fatalf("expected custom template not to receive default resource/script guidance, got %q", instructions)
	}
}

func TestProvider_CustomPromptTemplate_LegacyInstructionPlaceholdersRemainLiteral(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Instructions.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		SkillsInstructionPrompt: new("Custom skills:\n{skills}\n{resource_instructions}\n{script_instructions}"),
		Skills:                  []*skills.Skill{skill},
	})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "{resource_instructions}") || !strings.Contains(instructions, "{script_instructions}") {
		t.Fatalf("expected legacy placeholders to remain literal, got %q", instructions)
	}
}

func providerFromFileSource(source *fsskills.Source, opts *skills.ContextProviderOptions) agent.ContextProvider {
	if opts == nil {
		return skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
	}
	resolved := *opts
	resolved.Sources = []skills.Source{source}
	return skills.NewContextProvider(resolved)
}

func TestProvider_FromFileSourceWithOptions_DiscoversSkills(t *testing.T) {
	for _, test := range []struct {
		name    string
		options *skills.ContextProviderOptions
	}{
		{name: "default caching"},
		{name: "caching disabled", options: &skills.ContextProviderOptions{DisableCaching: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			createSkillDir(t, root, "opts-skill", "Options skill", "Options body.")
			provider := providerFromFileSource(
				fsskills.NewSourceOptions(fsskills.SourceOptions{
					ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
						return nil, nil
					},
				}, os.DirFS(root)),
				test.options,
			)

			instructions, _ := captureProviderContext(t, provider)
			if !strings.Contains(instructions, "opts-skill") {
				t.Fatalf("expected opts-skill in instructions, got %q", instructions)
			}
		})
	}
}

func TestProvider_FromFileSourceWithMultipleFileSystems_DiscoversMultipleSkills(t *testing.T) {
	for _, test := range []struct {
		name    string
		options *skills.ContextProviderOptions
	}{
		{name: "default caching"},
		{name: "caching disabled", options: &skills.ContextProviderOptions{DisableCaching: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			createSkillDir(t, filepath.Join(root, "multi-opts-1"), "skill-x", "Skill X", "Body X.")
			createSkillDir(t, filepath.Join(root, "multi-opts-2"), "skill-y", "Skill Y", "Body Y.")
			provider := providerFromFileSource(
				fsskills.NewSourceOptions(fsskills.SourceOptions{
					ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
						return nil, nil
					},
				}, os.DirFS(filepath.Join(root, "multi-opts-1")), os.DirFS(filepath.Join(root, "multi-opts-2"))),
				test.options,
			)

			instructions, _ := captureProviderContext(t, provider)
			if !strings.Contains(instructions, "skill-x") || !strings.Contains(instructions, "skill-y") {
				t.Fatalf("expected both skills in instructions, got %q", instructions)
			}
		})
	}
}

func TestProvider_DefaultApproval_RequiresAllSkillTools(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "approval-skill", "Approval test", "Body.")
	createRelativeFile(t, filepath.Join(root, "approval-skill"), "docs/readme.md", "hello")
	createRelativeFile(t, filepath.Join(root, "approval-skill"), "scripts/run.py", "print('hello')")
	provider := newProviderWithConfig(t, &fsskills.SourceOptions{ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
		return "ok", nil
	}}, nil, root)

	_, tools := captureProviderContext(t, provider)
	for _, name := range []string{loadSkillToolName, readSkillResourceToolName, runSkillScriptToolName} {
		assertToolApprovalRequired(t, findTool(t, tools, name), true)
	}
}

func TestProvider_ReadOnlyToolsAutoApprovalRule(t *testing.T) {
	tests := []struct {
		name string
		call *message.FunctionCallContent
		want bool
	}{
		{
			name: "load_skill",
			call: &message.FunctionCallContent{Name: loadSkillToolName},
			want: true,
		},
		{
			name: "read_skill_resource",
			call: &message.FunctionCallContent{Name: readSkillResourceToolName},
			want: true,
		},
		{
			name: "run_skill_script",
			call: &message.FunctionCallContent{Name: runSkillScriptToolName},
			want: false,
		},
		{
			name: "other tool",
			call: &message.FunctionCallContent{Name: "weather"},
			want: false,
		},
		{
			name: "nil function call",
			call: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := skills.ReadOnlyToolsAutoApprovalRule(t.Context(), &toolapproval.ToolAutoApprovalRuleContext{FunctionCall: tt.call})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %t, got %t", tt.want, got)
			}
		})
	}
}

func TestProvider_AllToolsAutoApprovalRule(t *testing.T) {
	tests := []struct {
		name string
		call *message.FunctionCallContent
		want bool
	}{
		{
			name: "load_skill",
			call: &message.FunctionCallContent{Name: loadSkillToolName},
			want: true,
		},
		{
			name: "read_skill_resource",
			call: &message.FunctionCallContent{Name: readSkillResourceToolName},
			want: true,
		},
		{
			name: "run_skill_script",
			call: &message.FunctionCallContent{Name: runSkillScriptToolName},
			want: true,
		},
		{
			name: "other tool",
			call: &message.FunctionCallContent{Name: "weather"},
			want: false,
		},
		{
			name: "nil function call",
			call: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := skills.AllToolsAutoApprovalRule(t.Context(), &toolapproval.ToolAutoApprovalRuleContext{FunctionCall: tt.call})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %t, got %t", tt.want, got)
			}
		})
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
	content, err := loadTool.Call(t.Context(), `{"skillName":"dup-skill"}`)
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
	content, err := loadTool.Call(t.Context(), `{"skillName":"dup-inline"}`)
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

	for range 2 {
		_, _ = captureProviderContext(t, provider)
	}

	if source.count != 1 {
		t.Fatalf("expected source to be loaded once, got %d", source.count)
	}
}

func TestProvider_CacheIsolationKey_LoadsEachBucketOnce(t *testing.T) {
	type tenantContextKey struct{}

	skill := mustInlineSkill(
		skills.Frontmatter{Name: "isolated-skill", Description: "Isolated skill"},
		"Instructions.",
		nil,
		nil,
	)
	source := &countingSource{skills: []*skills.Skill{skill}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{source},
		CacheIsolationKeySelector: func(ctx context.Context, invoking agent.InvokingContext) *string {
			key, _ := ctx.Value(tenantContextKey{}).(string)
			if len(invoking.Messages) > 0 {
				key += ":with-messages"
			}
			return &key
		},
	})

	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		ctx := context.WithValue(t.Context(), tenantContextKey{}, tenant)
		_, _, _ = invokeProvider(provider, ctx, nil)
		_, _, _ = invokeProvider(provider, ctx, nil)
		messages := []*message.Message{message.NewText("request")}
		_, _, _ = invokeProvider(provider, ctx, messages)
		_, _, _ = invokeProvider(provider, ctx, messages)
	}

	if source.count != 4 {
		t.Fatalf("expected one source load per cache bucket, got %d", source.count)
	}
}

func TestProvider_ZeroRefreshInterval_RefreshesEveryInvocation(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "refreshing-skill", Description: "Refreshing skill"},
		"Instructions.",
		nil,
		nil,
	)
	source := &countingSource{skills: []*skills.Skill{skill}}
	interval := time.Duration(0)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:         []skills.Source{source},
		RefreshInterval: &interval,
	})

	for range 2 {
		if _, _, err := invokeProvider(provider, t.Context(), nil); err != nil {
			t.Fatal(err)
		}
	}

	if source.count != 2 {
		t.Fatalf("expected zero refresh interval to reload each time, got %d loads", source.count)
	}
}

func TestProvider_PositiveRefreshInterval_ReusesUntilExpired(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "refreshing-skill", Description: "Refreshing skill"},
		"Instructions.",
		nil,
		nil,
	)
	source := &countingSource{skills: []*skills.Skill{skill}}
	interval := time.Second
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:         []skills.Source{source},
		RefreshInterval: &interval,
	})

	_, _, _ = invokeProvider(provider, t.Context(), nil)
	_, _, _ = invokeProvider(provider, t.Context(), nil)
	if source.count != 1 {
		t.Fatalf("expected cached source result before expiry, got %d loads", source.count)
	}

	time.Sleep(interval + 10*time.Millisecond)
	_, _, _ = invokeProvider(provider, t.Context(), nil)
	if source.count != 2 {
		t.Fatalf("expected source to reload after expiry, got %d loads", source.count)
	}
}

// A caller waiting for another invocation to populate the cache must still
// observe its own context cancellation promptly.
func TestProvider_CanceledWaiterDoesNotBlockOnSharedLoad(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "blocking-skill", Description: "Blocking skill"},
		"Instructions.",
		nil,
		nil,
	)
	source := &blockingSource{
		started: make(chan struct{}),
		release: make(chan struct{}),
		skill:   skill,
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	ownerDone := make(chan error, 1)
	go func() {
		_, _, err := invokeProvider(provider, context.Background(), nil)
		ownerDone <- err
	}()
	<-source.started

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	waiterDone := make(chan error, 1)
	go func() {
		_, _, err := invokeProvider(provider, canceledCtx, nil)
		waiterDone <- err
	}()

	var waiterErr error
	select {
	case waiterErr = <-waiterDone:
	case <-time.After(250 * time.Millisecond):
		source.unblock()
		if err := <-ownerDone; err != nil {
			t.Fatalf("cache owner: %v", err)
		}
		waiterErr = <-waiterDone
		t.Fatalf("canceled provider invocation remained blocked until the shared load completed; final error: %v", waiterErr)
	}
	source.unblock()
	if err := <-ownerDone; err != nil {
		t.Fatalf("cache owner: %v", err)
	}
	if !errors.Is(waiterErr, context.Canceled) {
		t.Fatalf("canceled provider invocation error = %v, want context.Canceled", waiterErr)
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
	_, _ = captureProviderContext(t, provider)

	if source.count != 3 {
		t.Fatalf("expected source to be loaded exactly three times, got %d", source.count)
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

	_, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
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
		_, options, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
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
		GetContent: func(context.Context) (string, error) {
			return "Should be skipped.", nil
		},
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
			GetContent: func(context.Context) (string, error) {
				return "Instructions.", nil
			},
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
	runTool := findTool(t, tools, runSkillScriptToolName)
	result, err := runTool.Call(t.Context(), `{"skillName":"script-skill","scriptName":"scripts/run.py","arguments":["--value","42"]}`)
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
	runTool := findTool(t, tools, runSkillScriptToolName)
	result, err := runTool.Call(t.Context(), `{"skillName":"script-skill","scriptName":"./scripts/run.py"}`)
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

func TestProvider_SkillMemberLookupErrors(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "lookup-skill", Description: "Lookup skill"},
		"Body.",
		[]skills.Resource{{Name: "docs/readme.md", Read: func(context.Context) (any, error) {
			return "docs", nil
		}}},
		[]skills.Script{{Name: "scripts/run", Run: func(context.Context, *skills.Skill, []string) (any, error) {
			return "ok", nil
		}}},
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})
	_, tools := captureProviderContext(t, provider)

	tests := []struct {
		name    string
		tool    string
		payload string
		want    string
	}{
		{
			name:    "empty resource name",
			tool:    "read_skill_resource",
			payload: `{"skillName":"lookup-skill","resourceName":" "}`,
			want:    "Error: Resource name cannot be empty.",
		},
		{
			name:    "missing resource",
			tool:    "read_skill_resource",
			payload: `{"skillName":"lookup-skill","resourceName":"missing.md"}`,
			want:    "Error: Resource 'missing.md' not found in skill 'lookup-skill'.",
		},
		{
			name:    "empty script name",
			tool:    "run_skill_script",
			payload: `{"skillName":"lookup-skill","scriptName":" "}`,
			want:    "Error: Script name cannot be empty.",
		},
		{
			name:    "missing script",
			tool:    "run_skill_script",
			payload: `{"skillName":"lookup-skill","scriptName":"missing"}`,
			want:    "Error: Script 'missing' not found in skill 'lookup-skill'.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := findTool(t, tools, tt.tool).Call(t.Context(), tt.payload)
			if err != nil {
				t.Fatalf("expected no tool error, got %v", err)
			}
			if result != tt.want {
				t.Fatalf("expected %q, got %#v", tt.want, result)
			}
		})
	}
}

func TestProvider_RunSkillScript_PropagatesErrorByDefault(t *testing.T) {
	wantErr := errors.New("boom-script")
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "script-skill", Description: "Script skill"},
		"Body.",
		nil,
		[]skills.Script{{
			Name: "explode",
			Run: func(context.Context, *skills.Skill, []string) (any, error) {
				return nil, wantErr
			},
		}},
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, runSkillScriptToolName)
	result, err := runTool.Call(t.Context(), `{"skillName":"script-skill","scriptName":"explode"}`)
	if err == nil {
		t.Fatalf("expected script error to propagate, got result %#v", result)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the original script error, got %v", err)
	}
	if err.Error() != "boom-script" {
		t.Fatalf("expected boom-script error, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected no result after script failure, got %#v", result)
	}
}

func TestProvider_RunSkillScript_PropagatesNilRunnerErrorByDefault(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "script-skill", Description: "Script skill"},
		"Body.",
		nil,
		[]skills.Script{{Name: "explode"}},
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, runSkillScriptToolName)
	result, err := runTool.Call(t.Context(), `{"skillName":"script-skill","scriptName":"explode"}`)
	if err == nil {
		t.Fatalf("expected nil-runner error to propagate, got result %#v", result)
	}
	if err.Error() != "script runner is nil" {
		t.Fatalf("expected script runner is nil error, got %v", err)
	}
}

func TestProvider_RunSkillScript_IncludesDetailsWhenEnabled(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "script-skill", Description: "Script skill"},
		"Body.",
		nil,
		[]skills.Script{{
			Name: "explode",
			Run: func(context.Context, *skills.Skill, []string) (any, error) {
				return nil, errors.New("boom-script")
			},
		}},
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills:                []*skills.Skill{skill},
		IncludeDetailedErrors: true,
	})

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, runSkillScriptToolName)
	result, err := runTool.Call(t.Context(), `{"skillName":"script-skill","scriptName":"explode"}`)
	if err != nil {
		t.Fatal(err)
	}
	const want = "Error: Failed to execute script 'explode' from skill 'script-skill'. Exception: boom-script"
	if result != want {
		t.Fatalf("expected detailed script error %q, got %#v", want, result)
	}
}

func TestProvider_RunSkillScript_IncludesNilRunnerDetailsWhenEnabled(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "script-skill", Description: "Script skill"},
		"Body.",
		nil,
		[]skills.Script{{Name: "explode"}},
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills:                []*skills.Skill{skill},
		IncludeDetailedErrors: true,
	})

	_, tools := captureProviderContext(t, provider)
	runTool := findTool(t, tools, "run_skill_script")
	result, err := runTool.Call(t.Context(), `{"skillName":"script-skill","scriptName":"explode"}`)
	if err != nil {
		t.Fatal(err)
	}
	const want = "Error: Failed to execute script 'explode' from skill 'script-skill'. Exception: script runner is nil"
	if result != want {
		t.Fatalf("expected detailed script error %q, got %#v", want, result)
	}
}

func TestProvider_DisableSkillToolApprovalOptions_DisableApprovalPerTool(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "approval-skill", "Approval skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "approval-skill"), "scripts/run.py", "print('ok')")
	createRelativeFile(t, filepath.Join(root, "approval-skill"), "docs/readme.md", "docs")

	provider := newProviderWithConfig(t, &fsskills.SourceOptions{
		ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
			return "ok", nil
		},
	}, &skills.ContextProviderOptions{
		DisableLoadSkillApproval:         true,
		DisableReadSkillResourceApproval: true,
		DisableRunSkillScriptApproval:    true,
	}, root)

	_, tools := captureProviderContext(t, provider)
	for _, name := range []string{loadSkillToolName, readSkillResourceToolName, runSkillScriptToolName} {
		assertToolApprovalRequired(t, findTool(t, tools, name), false)
	}
}

func TestProvider_DisableSkillToolApprovalOptions_AffectOnlyTargetTool(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "approval-skill", "Approval skill", "Body.")
	createRelativeFile(t, filepath.Join(root, "approval-skill"), "scripts/run.py", "print('ok')")
	createRelativeFile(t, filepath.Join(root, "approval-skill"), "docs/readme.md", "docs")

	tests := []struct {
		name string
		opts skills.ContextProviderOptions
		want map[string]bool
	}{
		{
			name: "load_skill",
			opts: skills.ContextProviderOptions{DisableLoadSkillApproval: true},
			want: map[string]bool{
				loadSkillToolName:         false,
				readSkillResourceToolName: true,
				runSkillScriptToolName:    true,
			},
		},
		{
			name: "read_skill_resource",
			opts: skills.ContextProviderOptions{DisableReadSkillResourceApproval: true},
			want: map[string]bool{
				loadSkillToolName:         true,
				readSkillResourceToolName: false,
				runSkillScriptToolName:    true,
			},
		},
		{
			name: "run_skill_script",
			opts: skills.ContextProviderOptions{DisableRunSkillScriptApproval: true},
			want: map[string]bool{
				loadSkillToolName:         true,
				readSkillResourceToolName: true,
				runSkillScriptToolName:    false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := newProviderWithConfig(t, &fsskills.SourceOptions{
				ScriptRunner: func(context.Context, *skills.Skill, *skills.Script, []string) (any, error) {
					return "ok", nil
				},
			}, &tt.opts, root)

			_, tools := captureProviderContext(t, provider)
			for name, want := range tt.want {
				assertToolApprovalRequired(t, findTool(t, tools, name), want)
			}
		})
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
	runTool := findTool(t, tools, runSkillScriptToolName)
	result, err := runTool.Call(t.Context(), `{"skillName":"runner-skill","scriptName":"scripts/run.py"}`)
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
	if len(tools) != 3 {
		t.Fatalf("expected load_skill, read_skill_resource, and run_skill_script tools, got %d tools", len(tools))
	}
	toolNames := make([]string, len(tools))
	for i, t2 := range tools {
		toolNames[i] = t2.Name()
	}
	for _, name := range []string{loadSkillToolName, readSkillResourceToolName, runSkillScriptToolName} {
		if !slices.Contains(toolNames, name) {
			t.Fatalf("expected %s tool to be present, got %v", name, toolNames)
		}
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

	messages, options, err := invokeProvider(provider, t.Context(), []*message.Message{input}, agent.WithSession(session))
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
	if len(tools) != 3 {
		t.Fatalf("expected 3 skill tools (load_skill, read_skill_resource, run_skill_script), got %d", len(tools))
	}
}

func TestProvider_WithEmptySource_ReturnsEmptyProvider(t *testing.T) {
	provider := skills.NewContextProvider(skills.ContextProviderOptions{})
	ctx := context.Background()
	messages, options, err := invokeProvider(provider, ctx, nil, agent.WithSession(agenttest.CreateSession()))
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
	result, err := loadTool.Call(t.Context(), `{"skillName":"drop-inline"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Error: Skill 'drop-inline' not found." {
		t.Fatalf("expected filtered inline skill to be unavailable, got %#v", result)
	}

	result, err = loadTool.Call(t.Context(), `{"skillName":"drop-source"}`)
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
	messages, options, err := invokeProvider(provider, ctx, nil, agent.WithSession(agenttest.CreateSession()))
	if err != nil {
		t.Fatal(err)
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	instructions, _ := agent.GetOption(options, agent.WithInstructions)
	if instructions != "" || len(messages) != 0 || len(tools) != 0 {
		t.Fatalf("expected empty provider context when filter removes all skills, got instructions=%q messages=%d tools=%d", instructions, len(messages), len(tools))
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

func TestLoadSkill_GetContentFunc_ReturnsContent(t *testing.T) {
	skill := &skills.Skill{
		Frontmatter: skills.Frontmatter{Name: "lazy-skill", Description: "Lazy content skill"},
		GetContent: func(context.Context) (string, error) {
			return "Lazy loaded content.", nil
		},
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})
	_, tools := captureProviderContext(t, provider)

	loadTool := findTool(t, tools, "load_skill")
	result, err := loadTool.Call(t.Context(), `{"skillName":"lazy-skill"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Lazy loaded content." {
		t.Fatalf("expected GetContent result, got %q", result)
	}
}

func TestLoadSkill_GetContentFunc_ErrorReturnsErrorMessage(t *testing.T) {
	skill := &skills.Skill{
		Frontmatter: skills.Frontmatter{Name: "failing-skill", Description: "Content always fails"},
		GetContent: func(context.Context) (string, error) {
			return "", errors.New("backend unavailable")
		},
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})
	_, tools := captureProviderContext(t, provider)

	loadTool := findTool(t, tools, "load_skill")
	result, err := loadTool.Call(t.Context(), `{"skillName":"failing-skill"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.HasPrefix(resultStr, "Error:") {
		t.Fatalf("expected error message, got %q", resultStr)
	}
}

func TestLoadSkill_GetContentFunc_NilReturnsErrorMessage(t *testing.T) {
	skill := &skills.Skill{
		Frontmatter: skills.Frontmatter{Name: "missing-content", Description: "Content loader missing"},
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})
	_, tools := captureProviderContext(t, provider)

	loadTool := findTool(t, tools, "load_skill")
	result, err := loadTool.Call(t.Context(), `{"skillName":"missing-content"}`)
	if err != nil {
		t.Fatal(err)
	}
	resultStr, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if !strings.HasPrefix(resultStr, "Error:") {
		t.Fatalf("expected error message, got %q", resultStr)
	}
}

func TestProvider_Cache_MultipleInvocations_LoadsOnce(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	_, first := captureProviderContext(t, provider)
	_, second := captureProviderContext(t, provider)
	if source.count != 1 {
		t.Fatalf("source calls = %d, want 1", source.count)
	}
	if funcToolPointer(t, findTool(t, first, loadSkillToolName)) != funcToolPointer(t, findTool(t, second, loadSkillToolName)) {
		t.Fatal("cached calls must reuse the same skill tools")
	}
}

func TestProvider_Cache_ConcurrentInvocations_LoadOnceAndShareTools(t *testing.T) {
	var calls atomic.Int32
	skill := mustInlineSkill(skills.Frontmatter{Name: "skill-b", Description: "B"}, "Instructions B.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{
		skills.SourceFunc(func(context.Context) ([]*skills.Skill, error) {
			calls.Add(1)
			return []*skills.Skill{skill}, nil
		}),
	}})
	type result struct {
		options []agent.Option
		err     error
	}
	results := make([]result, 10)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			_, results[i].options, results[i].err = invokeProvider(provider, t.Context(), nil)
		})
	}
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("source calls = %d, want 1", calls.Load())
	}
	var first uintptr
	for i, result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		tools := slices.Collect(agent.AllOptions(result.options, agent.WithTool))
		current := funcToolPointer(t, findTool(t, tools, loadSkillToolName))
		if i == 0 {
			first = current
		} else if current != first {
			t.Fatal("concurrent cached calls returned different skill tools")
		}
	}
}

func TestProvider_Cache_SourceErrorIsNotCached(t *testing.T) {
	calls := 0
	failure := errors.New("Simulated failure.")
	source := skills.SourceFunc(func(context.Context) ([]*skills.Skill, error) {
		calls++
		if calls == 1 {
			return nil, failure
		}
		return []*skills.Skill{
			mustInlineSkill(skills.Frontmatter{Name: "recovered-skill", Description: "Recovered"}, "Body.", nil, nil),
		}, nil
	})
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	if _, _, err := invokeProvider(provider, t.Context(), nil); !errors.Is(err, failure) {
		t.Fatalf("first invocation error = %v, want original failure", err)
	}
	instructions, _ := captureProviderContext(t, provider)
	if strings.Count(instructions, "<skill>") != 1 || !strings.Contains(instructions, "<name>recovered-skill</name>") {
		t.Fatalf("retry instructions = %q, want recovered skill", instructions)
	}
	if calls != 2 {
		t.Fatalf("source calls = %d, want 2", calls)
	}
}

func TestProvider_Cache_ReturnsSourceSkills(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-x", Description: "X"}, "Body X.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "skill-y", Description: "Y"}, "Body Y.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	instructions, _ := captureProviderContext(t, provider)
	if strings.Count(instructions, "<skill>") != 2 {
		t.Fatalf("instructions = %q, want exactly two source skills", instructions)
	}
	first := strings.Index(instructions, "<name>skill-x</name>")
	second := strings.Index(instructions, "<name>skill-y</name>")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("source skill order not preserved in prompt: %q", instructions)
	}
}

func TestProvider_Cache_SameIsolationKeySharesResult(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{source},
		CacheIsolationKeySelector: func(context.Context, agent.InvokingContext) *string {
			return new("same-key")
		},
	})

	_, firstOptions, firstErr := invokeProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
	_, secondOptions, secondErr := invokeProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
	if firstErr != nil || secondErr != nil {
		t.Fatalf("invocation errors = %v, %v", firstErr, secondErr)
	}
	first := slices.Collect(agent.AllOptions(firstOptions, agent.WithTool))
	second := slices.Collect(agent.AllOptions(secondOptions, agent.WithTool))
	if source.count != 1 || funcToolPointer(t, findTool(t, first, loadSkillToolName)) != funcToolPointer(t, findTool(t, second, loadSkillToolName)) {
		t.Fatalf("same isolation key did not share the cache; source calls = %d", source.count)
	}
}

func TestProvider_Cache_DifferentIsolationKeysLoadSeparately(t *testing.T) {
	firstSession, secondSession := agenttest.CreateSession(), agenttest.CreateSession()
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{source},
		CacheIsolationKeySelector: func(_ context.Context, invoking agent.InvokingContext) *string {
			session, _ := agent.GetOption(invoking.Options, agent.WithSession)
			if session == firstSession {
				return new("agent-1")
			}
			return new("agent-2")
		},
	})

	for _, session := range []*agent.Session{firstSession, secondSession} {
		if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
			t.Fatal(err)
		}
	}
	if source.count != 2 {
		t.Fatalf("source calls = %d, want 2", source.count)
	}
}

func TestProvider_Cache_NilIsolationKeyUsesSharedResult(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{source},
		CacheIsolationKeySelector: func(context.Context, agent.InvokingContext) *string {
			return nil
		},
	})

	_, firstOptions, firstErr := invokeProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
	_, secondOptions, secondErr := invokeProvider(provider, t.Context(), nil, agent.WithSession(agenttest.CreateSession()))
	if firstErr != nil || secondErr != nil {
		t.Fatalf("invocation errors = %v, %v", firstErr, secondErr)
	}
	first := slices.Collect(agent.AllOptions(firstOptions, agent.WithTool))
	second := slices.Collect(agent.AllOptions(secondOptions, agent.WithTool))
	if source.count != 1 || funcToolPointer(t, findTool(t, first, loadSkillToolName)) != funcToolPointer(t, findTool(t, second, loadSkillToolName)) {
		t.Fatalf("nil isolation keys did not share the cache; source calls = %d", source.count)
	}
}

func TestProvider_Cache_EmptyIsolationKeyIsDistinctFromSharedCache(t *testing.T) {
	var key *string
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:                   []skills.Source{source},
		CacheIsolationKeySelector: func(context.Context, agent.InvokingContext) *string { return key },
	})

	captureProviderContext(t, provider)
	key = new("")
	captureProviderContext(t, provider)
	if source.count != 2 {
		t.Fatalf("source calls = %d, want separate shared and empty-key buckets", source.count)
	}
}

type gatedCacheSource struct {
	calls   atomic.Int32
	started chan context.Context
	release chan struct{}
}

func (s *gatedCacheSource) Skills(ctx context.Context) ([]*skills.Skill, error) {
	s.calls.Add(1)
	s.started <- ctx
	select {
	case <-s.release:
		return []*skills.Skill{
			mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestProvider_Cache_CancelingOwnerRestartsWaitingFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &gatedCacheSource{started: make(chan context.Context, 2), release: make(chan struct{})}
		provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ownerDone := make(chan error, 1)
		go func() {
			_, _, err := invokeProvider(provider, ctx, nil)
			ownerDone <- err
		}()
		<-source.started
		type result struct {
			options []agent.Option
			err     error
		}
		waiterDone := make(chan result, 1)
		go func() {
			_, options, err := invokeProvider(provider, t.Context(), nil)
			waiterDone <- result{options, err}
		}()
		synctest.Wait()
		cancel()
		if err := <-ownerDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("owner error = %v, want cancellation", err)
		}
		waiterContext := <-source.started
		close(source.release)
		got := <-waiterDone
		if got.err != nil {
			t.Fatal(got.err)
		}
		instructions, _ := agent.GetOption(got.options, agent.WithInstructions)
		if strings.Count(instructions, "<skill>") != 1 || !strings.Contains(instructions, "<name>skill-a</name>") {
			t.Fatalf("waiter instructions = %q, want skill-a", instructions)
		}
		if source.calls.Load() != 2 || waiterContext.Err() != nil {
			t.Fatalf("fetch calls = %d, waiter error = %v", source.calls.Load(), waiterContext.Err())
		}
	})
}

func TestProvider_Cache_AllCallersCancel_CancelsSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &gatedCacheSource{started: make(chan context.Context, 2), release: make(chan struct{})}
		provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
		first, cancelFirst := context.WithCancel(t.Context())
		defer cancelFirst()
		second, cancelSecond := context.WithCancel(t.Context())
		defer cancelSecond()
		done := make(chan error, 2)
		go func() {
			_, _, err := invokeProvider(provider, first, nil)
			done <- err
		}()
		sourceContext := <-source.started
		go func() {
			_, _, err := invokeProvider(provider, second, nil)
			done <- err
		}()
		synctest.Wait()
		cancelFirst()
		cancelSecond()
		for range 2 {
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("invocation error = %v, want cancellation", err)
			}
		}
		if !errors.Is(sourceContext.Err(), context.Canceled) {
			t.Fatalf("source context error = %v, want cancellation", sourceContext.Err())
		}
	})
}

func TestProvider_Cache_SingleCallerCancels_CancelsSource(t *testing.T) {
	source := &gatedCacheSource{started: make(chan context.Context, 1), release: make(chan struct{})}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := invokeProvider(provider, ctx, nil)
		done <- err
	}()
	sourceContext := <-source.started
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("invocation error = %v, want cancellation", err)
	}
	if !errors.Is(sourceContext.Err(), context.Canceled) {
		t.Fatalf("source context error = %v, want cancellation", sourceContext.Err())
	}
}

func TestProvider_Cache_CanceledFetchDoesNotPoisonNextCall(t *testing.T) {
	source := &gatedCacheSource{started: make(chan context.Context, 2), release: make(chan struct{})}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := invokeProvider(provider, ctx, nil)
		done <- err
	}()
	<-source.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("first invocation error = %v, want cancellation", err)
	}
	close(source.release)
	instructions, _ := captureProviderContext(t, provider)
	if strings.Count(instructions, "<skill>") != 1 || source.calls.Load() != 2 {
		t.Fatalf("retry instructions = %q, source calls = %d", instructions, source.calls.Load())
	}
}

func TestProvider_Cache_WithinRefreshIntervalSharesResult(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:         []skills.Source{source},
		RefreshInterval: new(time.Hour),
	})

	_, first := captureProviderContext(t, provider)
	_, second := captureProviderContext(t, provider)
	if source.count != 1 || funcToolPointer(t, findTool(t, first, loadSkillToolName)) != funcToolPointer(t, findTool(t, second, loadSkillToolName)) {
		t.Fatalf("fresh cache was not reused; source calls = %d", source.count)
	}
}

func TestProvider_Cache_NegativeRefreshIntervalRefetches(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:         []skills.Source{source},
		RefreshInterval: new(-time.Minute),
	})

	captureProviderContext(t, provider)
	captureProviderContext(t, provider)
	if source.count != 2 {
		t.Fatalf("source calls = %d, want 2", source.count)
	}
}

func TestProvider_Cache_NilRefreshIntervalNeverExpires(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:         []skills.Source{source},
		RefreshInterval: nil,
	})

	_, first := captureProviderContext(t, provider)
	_, second := captureProviderContext(t, provider)
	if source.count != 1 || funcToolPointer(t, findTool(t, first, loadSkillToolName)) != funcToolPointer(t, findTool(t, second, loadSkillToolName)) {
		t.Fatalf("non-expiring cache was not reused; source calls = %d", source.count)
	}
}

type advertisedSkill struct {
	Name        string `xml:"name"`
	Description string `xml:"description"`
}

func advertisedSkills(t *testing.T, instructions string) []advertisedSkill {
	t.Helper()
	if instructions == "" {
		return nil
	}
	_, tail, ok := strings.Cut(instructions, "<available_skills>")
	if !ok {
		t.Fatalf("available_skills block missing from %q", instructions)
	}
	body, _, ok := strings.Cut(tail, "</available_skills>")
	if !ok {
		t.Fatalf("available_skills closing tag missing from %q", instructions)
	}
	var block struct {
		Skills []advertisedSkill `xml:"skill"`
	}
	if err := xml.Unmarshal([]byte("<available_skills>"+body+"</available_skills>"), &block); err != nil {
		t.Fatal(err)
	}
	return block.Skills
}

func TestProvider_BuildWithoutSources_Succeeds(t *testing.T) {
	provider := skills.NewContextProvider(skills.ContextProviderOptions{})
	if provider == nil {
		t.Fatal("expected a provider without configured sources")
	}
}

func TestProvider_BuildWithCustomSource_Succeeds(t *testing.T) {
	source := skills.NewInMemorySource(mustInlineSkill(
		skills.Frontmatter{Name: "custom", Description: "Custom skill"}, "Instructions.", nil, nil,
	))
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
	if provider == nil {
		t.Fatal("expected a provider with a custom source")
	}
}

func TestProvider_NilConfiguredSource_Panics(t *testing.T) {
	defer func() {
		if got := recover(); got != "source 0 is nil" {
			t.Fatalf("panic = %v, want indexed nil source error", got)
		}
	}()
	skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{nil}})
}

func TestProvider_BuildWithFilter_AppliesToSourceSkills(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "keep-me", Description: "Keep"}, "Instructions.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "drop-me", Description: "Drop"}, "Instructions.", nil, nil),
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:     []skills.Source{source},
		SkillFilter: func(skill *skills.Skill) bool { return strings.HasPrefix(skill.Frontmatter.Name, "keep") },
	})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "keep-me") || strings.Contains(instructions, "drop-me") {
		t.Fatalf("filtered instructions = %q", instructions)
	}
}

func TestProvider_BuildWithIsolationKey_CachesPerInvocationIdentity(t *testing.T) {
	firstSession, secondSession := agenttest.CreateSession(), agenttest.CreateSession()
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{source},
		CacheIsolationKeySelector: func(_ context.Context, invoking agent.InvokingContext) *string {
			session, _ := agent.GetOption(invoking.Options, agent.WithSession)
			if session == firstSession {
				return new("agent-1")
			}
			return new("agent-2")
		},
	})

	for _, session := range []*agent.Session{firstSession, firstSession, secondSession} {
		if _, _, err := invokeProvider(provider, t.Context(), nil, agent.WithSession(session)); err != nil {
			t.Fatal(err)
		}
	}
	if source.count != 2 {
		t.Fatalf("source calls = %d, want 2", source.count)
	}
}

func TestProvider_BuildOptions_CanEnableDetailedErrors(t *testing.T) {
	source := skills.NewInMemorySource(mustInlineSkill(
		skills.Frontmatter{Name: "test", Description: "Test"}, "Instructions.", nil, nil,
	))
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:               []skills.Source{source},
		IncludeDetailedErrors: true,
	})
	if provider == nil {
		t.Fatal("expected provider construction to accept detailed errors")
	}
}

func TestProvider_BuildWithMultipleCustomSources_AggregatesAll(t *testing.T) {
	first := skills.NewInMemorySource(mustInlineSkill(
		skills.Frontmatter{Name: "from-one", Description: "Source 1"}, "Instructions 1.", nil, nil,
	))
	second := skills.NewInMemorySource(mustInlineSkill(
		skills.Frontmatter{Name: "from-two", Description: "Source 2"}, "Instructions 2.", nil, nil,
	))
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{first, second}})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "from-one") || !strings.Contains(instructions, "from-two") {
		t.Fatalf("aggregated instructions = %q", instructions)
	}
}

func TestProvider_Filter_IncludingAllPreservesBothSkills(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "skill-b", Description: "B"}, "Instructions B.", nil, nil),
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:     []skills.Source{source},
		SkillFilter: func(*skills.Skill) bool { return true },
	})

	instructions, _ := captureProviderContext(t, provider)
	if got := advertisedSkills(t, instructions); len(got) != 2 {
		t.Fatalf("advertised skills = %#v, want both input skills", got)
	}
}

func TestProvider_Filter_ExcludingAllReturnsEmptyContext(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "skill-b", Description: "B"}, "Instructions B.", nil, nil),
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:     []skills.Source{source},
		SkillFilter: func(*skills.Skill) bool { return false },
	})

	instructions, tools := captureProviderContext(t, provider)
	if instructions != "" || len(tools) != 0 {
		t.Fatalf("excluded skills produced instructions %q or tools %v", instructions, tools)
	}
}

func TestProvider_Filter_ReturnsOnlyMatchingSkills(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "keep-me", Description: "Keep"}, "Instructions.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "drop-me", Description: "Drop"}, "Instructions.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "keep-also", Description: "KeepAlso"}, "Instructions.", nil, nil),
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:     []skills.Source{source},
		SkillFilter: func(skill *skills.Skill) bool { return strings.HasPrefix(skill.Frontmatter.Name, "keep") },
	})

	instructions, _ := captureProviderContext(t, provider)
	got := advertisedSkills(t, instructions)
	if len(got) != 2 {
		t.Fatalf("advertised skills = %#v, want 2", got)
	}
	for _, skill := range got {
		if !strings.HasPrefix(skill.Name, "keep") {
			t.Fatalf("unmatched skill advertised: %#v", skill)
		}
	}
}

func TestProvider_Filter_EmptySourceReturnsEmptyContext(t *testing.T) {
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:     []skills.Source{skills.NewInMemorySource()},
		SkillFilter: func(*skills.Skill) bool { return true },
	})

	instructions, tools := captureProviderContext(t, provider)
	if instructions != "" || len(tools) != 0 {
		t.Fatalf("empty source produced instructions %q or tools %v", instructions, tools)
	}
}

func TestProvider_Filter_NilSourcePanics(t *testing.T) {
	defer func() {
		if got := recover(); got != "source 0 is nil" {
			t.Fatalf("panic = %v, want indexed nil source error", got)
		}
	}()
	skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:     []skills.Source{nil},
		SkillFilter: func(*skills.Skill) bool { return true },
	})
}

func TestProvider_Filter_PreservesMatchingOrder(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "alpha", Description: "Alpha"}, "Instructions.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "beta", Description: "Beta"}, "Instructions.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "gamma", Description: "Gamma"}, "Instructions.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "delta", Description: "Delta"}, "Instructions.", nil, nil),
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{source},
		SkillFilter: func(skill *skills.Skill) bool {
			return skill.Frontmatter.Name == "alpha" || skill.Frontmatter.Name == "gamma"
		},
	})

	instructions, _ := captureProviderContext(t, provider)
	got := advertisedSkills(t, instructions)
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "gamma" {
		t.Fatalf("advertised skills = %#v, want alpha followed by gamma", got)
	}
}

func TestProvider_Deduplication_NoDuplicatesReturnsBothSkills(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "skill-a", Description: "A"}, "Instructions A.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "skill-b", Description: "B"}, "Instructions B.", nil, nil),
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	instructions, _ := captureProviderContext(t, provider)
	if got := advertisedSkills(t, instructions); len(got) != 2 {
		t.Fatalf("advertised skills = %#v, want 2", got)
	}
}

func TestProvider_Deduplication_KeepsFirstAndRetainsUniqueSkill(t *testing.T) {
	source := skills.NewInMemorySource(
		mustInlineSkill(skills.Frontmatter{Name: "dupe", Description: "First"}, "Instructions 1.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "dupe", Description: "Second"}, "Instructions 2.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "unique", Description: "Unique"}, "Instructions 3.", nil, nil),
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	instructions, _ := captureProviderContext(t, provider)
	got := advertisedSkills(t, instructions)
	want := []advertisedSkill{{Name: "dupe", Description: "First"}, {Name: "unique", Description: "Unique"}}
	if !slices.Equal(got, want) {
		t.Fatalf("advertised skills = %#v, want %#v", got, want)
	}
}

func TestProvider_Deduplication_CustomSourceKeepsFirst(t *testing.T) {
	source := skills.SourceFunc(func(context.Context) ([]*skills.Skill, error) {
		return []*skills.Skill{
			mustInlineSkill(skills.Frontmatter{Name: "my-skill", Description: "First"}, "Instructions 1.", nil, nil),
			mustInlineSkill(skills.Frontmatter{Name: "my-skill", Description: "Second"}, "Instructions 2.", nil, nil),
		}, nil
	})
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	instructions, _ := captureProviderContext(t, provider)
	got := advertisedSkills(t, instructions)
	if len(got) != 1 || got[0].Description != "First" {
		t.Fatalf("advertised skills = %#v, want only the first duplicate", got)
	}
}

func TestProvider_Deduplication_EmptySourceReturnsNoSkills(t *testing.T) {
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources: []skills.Source{skills.NewInMemorySource()},
	})

	instructions, tools := captureProviderContext(t, provider)
	if instructions != "" || len(tools) != 0 {
		t.Fatalf("empty source produced instructions %q or tools %v", instructions, tools)
	}
}

func TestProvider_EmptyFileSource_PreservesInputInstructions(t *testing.T) {
	provider := newProvider(t, t.TempDir())

	_, options, err := invokeProvider(provider, t.Context(), nil, agent.WithInstructions("Original instructions"))
	if err != nil {
		t.Fatal(err)
	}
	instructions, _ := agent.GetOption(options, agent.WithInstructions)
	if instructions != "Original instructions" {
		t.Fatalf("instructions = %q, want original instructions", instructions)
	}
	if got := slices.Collect(agent.AllOptions(options, agent.WithTool)); len(got) != 0 {
		t.Fatalf("empty source added tools: %v", got)
	}
}

func TestProvider_FileSkills_AppendsInstructionsAndTools(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "provider-skill", "Provider skill test", "Skill instructions body.")
	provider := newProvider(t, root)

	_, options, err := invokeProvider(provider, t.Context(), nil, agent.WithInstructions("Base instructions"))
	if err != nil {
		t.Fatal(err)
	}
	instructions := strings.Join(slices.Collect(agent.AllOptions(options, agent.WithInstructions)), "\n")
	for _, want := range []string{"Base instructions", "provider-skill", "Provider skill test"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions = %q, want %q", instructions, want)
		}
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	for _, name := range []string{loadSkillToolName, readSkillResourceToolName, runSkillScriptToolName} {
		findTool(t, tools, name)
	}
}

func TestProvider_PromptWithOnlySkillsPlaceholder_Succeeds(t *testing.T) {
	provider := newProviderWithConfig(t, nil, &skills.ContextProviderOptions{
		SkillsInstructionPrompt: new("Skills: {skills}"),
	}, t.TempDir())
	if provider == nil {
		t.Fatal("expected a provider with only the skills placeholder")
	}
}

func TestProvider_FileSource_LegacyPlaceholdersRemainLiteral(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "literal-test-skill", "Literal test", "Body.")
	provider := newProviderWithConfig(t, nil, &skills.ContextProviderOptions{
		SkillsInstructionPrompt: new("Skills: {skills}\nRes: {resource_instructions}\nScript: {script_instructions}"),
	}, root)

	instructions, _ := captureProviderContext(t, provider)
	for _, want := range []string{"{resource_instructions}", "{script_instructions}"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions = %q, want literal %q", instructions, want)
		}
	}
}

func TestProvider_FileSkills_PreservesExistingInputTools(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "tools-skill", "Tools test", "Body.")
	provider := newProvider(t, root)
	existing := functool.MustNew(functool.Config{Name: "existing_tool", Description: "An existing tool."}, func(context.Context, struct{}) (string, error) {
		return "test", nil
	})

	_, options, err := invokeProvider(provider, t.Context(), nil, agent.WithTool(existing))
	if err != nil {
		t.Fatal(err)
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	findTool(t, tools, "existing_tool")
	findTool(t, tools, loadSkillToolName)
}

func TestProvider_ConcurrentCalls_LoadSourceOnce(t *testing.T) {
	var calls atomic.Int32
	source := skills.SourceFunc(func(context.Context) ([]*skills.Skill, error) {
		calls.Add(1)
		return []*skills.Skill{
			mustInlineSkill(skills.Frontmatter{Name: "concurrent-skill", Description: "Concurrent test"}, "Body.", nil, nil),
		}, nil
	})
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})
	failures := make(chan error, 10)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_, _, err := invokeProvider(provider, t.Context(), nil)
			failures <- err
		})
	}
	wg.Wait()
	for range 10 {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("source calls = %d, want 1", calls.Load())
	}
}

func TestProvider_FileSkillWithScript_IncludesRunAndLoadTools(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "script-skill", "Skill with scripts", "Body.")
	createRelativeFile(t, filepath.Join(root, "script-skill"), "scripts/test.py", "print('hello')")
	provider := newProvider(t, root)

	_, tools := captureProviderContext(t, provider)
	findTool(t, tools, runSkillScriptToolName)
	findTool(t, tools, loadSkillToolName)
}

func TestProvider_InlineResource_IncludesReadResourceTool(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "res-skill", Description: "Has resources"}, "Body.",
		[]skills.Resource{{Name: "config", Description: "A config resource.", Read: func(context.Context) (any, error) { return "value1", nil }}},
		nil,
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})

	_, tools := captureProviderContext(t, provider)
	findTool(t, tools, readSkillResourceToolName)
}

func TestProvider_LoadSkill_EmptyNameReturnsError(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "any-skill", "Test", "Body.")
	_, tools := captureProviderContext(t, newProvider(t, root))

	got, err := findTool(t, tools, loadSkillToolName).Call(t.Context(), `{"skillName":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Error: Skill name cannot be empty." {
		t.Fatalf("load_skill result = %#v", got)
	}
}

func TestProvider_ReadInlineResource_ReturnsContent(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "res-skill", Description: "Has resources"}, "Body.",
		[]skills.Resource{{Name: "config", Read: func(context.Context) (any, error) { return "resource-value", nil }}}, nil,
	)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}})
	_, tools := captureProviderContext(t, provider)

	got, err := findTool(t, tools, readSkillResourceToolName).Call(t.Context(), `{"skillName":"res-skill","resourceName":"config"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "resource-value" {
		t.Fatalf("read_skill_resource result = %#v, want resource-value", got)
	}
}

func TestProvider_ReadResource_EmptySkillNameReturnsError(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "res-skill", Description: "Has resources"}, "Body.",
		[]skills.Resource{{Name: "config", Read: func(context.Context) (any, error) { return "v", nil }}}, nil,
	)
	_, tools := captureProviderContext(t, skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}}))

	got, err := findTool(t, tools, readSkillResourceToolName).Call(t.Context(), `{"skillName":"","resourceName":"config"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Error: Skill name cannot be empty." {
		t.Fatalf("read_skill_resource result = %#v", got)
	}
}

func TestProvider_ReadResource_EmptyResourceNameReturnsError(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "res-skill", Description: "Has resources"}, "Body.",
		[]skills.Resource{{Name: "config", Read: func(context.Context) (any, error) { return "v", nil }}}, nil,
	)
	_, tools := captureProviderContext(t, skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}}))

	got, err := findTool(t, tools, readSkillResourceToolName).Call(t.Context(), `{"skillName":"res-skill","resourceName":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Error: Resource name cannot be empty." {
		t.Fatalf("read_skill_resource result = %#v", got)
	}
}

func TestProvider_ReadResource_MissingSkillReturnsError(t *testing.T) {
	skill := mustInlineSkill(
		skills.Frontmatter{Name: "res-skill", Description: "Has resources"}, "Body.",
		[]skills.Resource{{Name: "config", Read: func(context.Context) (any, error) { return "v", nil }}}, nil,
	)
	_, tools := captureProviderContext(t, skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{skill}}))

	got, err := findTool(t, tools, readSkillResourceToolName).Call(t.Context(), `{"skillName":"non-existent","resourceName":"config"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Error: Skill 'non-existent' not found." {
		t.Fatalf("read_skill_resource result = %#v", got)
	}
}

func TestProvider_RunScript_EmptySkillNameReturnsError(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "err-script-skill", "Test", "Body.")
	createRelativeFile(t, filepath.Join(root, "err-script-skill"), "scripts/run.py", "print('hi')")
	_, tools := captureProviderContext(t, newProvider(t, root))

	got, err := findTool(t, tools, runSkillScriptToolName).Call(t.Context(), `{"skillName":"","scriptName":"scripts/run.py"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Error: Skill name cannot be empty." {
		t.Fatalf("run_skill_script result = %#v", got)
	}
}

func TestProvider_RunScript_EmptyScriptNameReturnsError(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "err-script2-skill", "Test", "Body.")
	createRelativeFile(t, filepath.Join(root, "err-script2-skill"), "scripts/run.py", "print('hi')")
	_, tools := captureProviderContext(t, newProvider(t, root))

	got, err := findTool(t, tools, runSkillScriptToolName).Call(t.Context(), `{"skillName":"err-script2-skill","scriptName":""}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Error: Script name cannot be empty." {
		t.Fatalf("run_skill_script result = %#v", got)
	}
}

func TestProvider_RunScript_MissingSkillReturnsError(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "err-script3-skill", "Test", "Body.")
	createRelativeFile(t, filepath.Join(root, "err-script3-skill"), "scripts/run.py", "print('hi')")
	_, tools := captureProviderContext(t, newProvider(t, root))

	got, err := findTool(t, tools, runSkillScriptToolName).Call(t.Context(), `{"skillName":"non-existent","scriptName":"scripts/run.py"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Error: Skill 'non-existent' not found." {
		t.Fatalf("run_skill_script result = %#v", got)
	}
}

func TestProvider_RunScript_MissingScriptReturnsError(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "err-script4-skill", "Test", "Body.")
	createRelativeFile(t, filepath.Join(root, "err-script4-skill"), "scripts/run.py", "print('hi')")
	_, tools := captureProviderContext(t, newProvider(t, root))

	got, err := findTool(t, tools, runSkillScriptToolName).Call(t.Context(), `{"skillName":"err-script4-skill","scriptName":"scripts/missing.py"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Error: Script 'scripts/missing.py' not found in skill 'err-script4-skill'." {
		t.Fatalf("run_skill_script result = %#v", got)
	}
}

func TestProvider_BuildWithCaching_CallsSourceOnce(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "cached-skill", Description: "Cached test"}, "Body.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	captureProviderContext(t, provider)
	captureProviderContext(t, provider)
	if source.count != 1 {
		t.Fatalf("source calls = %d, want 1", source.count)
	}
}

func TestProvider_BuildDefaultOptions_CachesSkills(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "default-skill", Description: "Default test"}, "Body.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	captureProviderContext(t, provider)
	captureProviderContext(t, provider)
	if source.count != 1 {
		t.Fatalf("source calls = %d, want 1", source.count)
	}
}

func TestProvider_MixedSources_AdvertisesInSortedOrder(t *testing.T) {
	root := t.TempDir()
	firstRoot, secondRoot := filepath.Join(root, "dir1"), filepath.Join(root, "dir2")
	createSkillDir(t, firstRoot, "file-skill-1", "First file skill", "Body 1.")
	createSkillDir(t, secondRoot, "file-skill-2", "Second file skill", "Body 2.")
	inline := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Body inline.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{
		fsskills.NewSource(os.DirFS(firstRoot)),
		skills.NewInMemorySource(inline),
		fsskills.NewSource(os.DirFS(secondRoot)),
	}})

	instructions, _ := captureProviderContext(t, provider)
	first, second, third := strings.Index(instructions, "file-skill-1"), strings.Index(instructions, "file-skill-2"), strings.Index(instructions, "inline-skill")
	if first < 0 || second < 0 || third < 0 || first >= second || second >= third {
		t.Fatalf("mixed source prompt is missing or misorders skills: %q", instructions)
	}
}

func TestProvider_MixedCustomInlineAndFileSources_DiscoverAllSkills(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "file-skill", "File skill", "Body file.")
	inline := mustInlineSkill(skills.Frontmatter{Name: "inline-skill", Description: "Inline skill"}, "Body inline.", nil, nil)
	custom := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "custom-skill", Description: "Custom source skill"}, "Body custom.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{
		custom, skills.NewInMemorySource(inline), fsskills.NewSource(os.DirFS(root)),
	}})

	instructions, _ := captureProviderContext(t, provider)
	for _, name := range []string{"custom-skill", "inline-skill", "file-skill"} {
		if !strings.Contains(instructions, name) {
			t.Fatalf("mixed source instructions = %q, want %s", instructions, name)
		}
	}
}

func TestProvider_DisableLoadApproval_LeavesOtherToolsProtected(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "test-skill", Description: "Test"}, "Body.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{skill}, DisableLoadSkillApproval: true,
	})

	_, tools := captureProviderContext(t, provider)
	assertToolApprovalRequired(t, findTool(t, tools, loadSkillToolName), false)
	assertToolApprovalRequired(t, findTool(t, tools, readSkillResourceToolName), true)
	assertToolApprovalRequired(t, findTool(t, tools, runSkillScriptToolName), true)
}

func TestProvider_DisableReadApproval_LeavesOtherToolsProtected(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "test-skill", Description: "Test"}, "Body.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{skill}, DisableReadSkillResourceApproval: true,
	})

	_, tools := captureProviderContext(t, provider)
	assertToolApprovalRequired(t, findTool(t, tools, readSkillResourceToolName), false)
	assertToolApprovalRequired(t, findTool(t, tools, loadSkillToolName), true)
	assertToolApprovalRequired(t, findTool(t, tools, runSkillScriptToolName), true)
}

func TestProvider_DisableScriptApproval_LeavesOtherToolsProtected(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "test-skill", Description: "Test"}, "Body.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills: []*skills.Skill{skill}, DisableRunSkillScriptApproval: true,
	})

	_, tools := captureProviderContext(t, provider)
	assertToolApprovalRequired(t, findTool(t, tools, runSkillScriptToolName), false)
	assertToolApprovalRequired(t, findTool(t, tools, loadSkillToolName), true)
	assertToolApprovalRequired(t, findTool(t, tools, readSkillResourceToolName), true)
}

func TestProvider_DisableAllApprovals_UnwrapsAllTools(t *testing.T) {
	skill := mustInlineSkill(skills.Frontmatter{Name: "test-skill", Description: "Test"}, "Body.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Skills:                           []*skills.Skill{skill},
		DisableLoadSkillApproval:         true,
		DisableReadSkillResourceApproval: true,
		DisableRunSkillScriptApproval:    true,
	})

	_, tools := captureProviderContext(t, provider)
	if len(tools) == 0 {
		t.Fatal("expected skill tools")
	}
	for _, name := range []string{loadSkillToolName, readSkillResourceToolName, runSkillScriptToolName} {
		assertToolApprovalRequired(t, findTool(t, tools, name), false)
	}
}

func TestProvider_CachedInvocations_ReturnToolsAndLoadSourceOnce(t *testing.T) {
	source := &countingSource{skills: []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "cached-tools-skill", Description: "Cached tools test"}, "Body.", nil, nil),
	}}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{source}})

	_, first := captureProviderContext(t, provider)
	_, second := captureProviderContext(t, provider)
	if len(first) == 0 || len(second) == 0 || source.count != 1 {
		t.Fatalf("cached tools counts = %d, %d; source calls = %d", len(first), len(second), source.count)
	}
}

func TestProvider_SingleFileDirectory_ProvidesSkillsAndLoadTool(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "file-ctor-skill", "File ctor test", "File body.")
	provider := newProvider(t, root)

	instructions, tools := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "file-ctor-skill") {
		t.Fatalf("instructions = %q, want file-ctor-skill", instructions)
	}
	findTool(t, tools, loadSkillToolName)
}

func TestProvider_MultipleFileDirectories_ProvidesAllSkills(t *testing.T) {
	root := t.TempDir()
	firstRoot, secondRoot := filepath.Join(root, "dir1"), filepath.Join(root, "dir2")
	createSkillDir(t, firstRoot, "skill-a", "Skill A", "Body A.")
	createSkillDir(t, secondRoot, "skill-b", "Skill B", "Body B.")
	provider := newProvider(t, firstRoot, secondRoot)

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "skill-a") || !strings.Contains(instructions, "skill-b") {
		t.Fatalf("instructions = %q, want skills from both directories", instructions)
	}
}

func TestProvider_InlineSkillArguments_ProvidesBothSkills(t *testing.T) {
	first := mustInlineSkill(skills.Frontmatter{Name: "inline-a", Description: "Inline A"}, "Instructions A.", nil, nil)
	second := mustInlineSkill(skills.Frontmatter{Name: "inline-b", Description: "Inline B"}, "Instructions B.", nil, nil)
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{skills.NewInMemorySource(first, second)}})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "inline-a") || !strings.Contains(instructions, "inline-b") {
		t.Fatalf("instructions = %q, want both inline skills", instructions)
	}
}

func TestProvider_InlineSkillSlice_ProvidesBothSkills(t *testing.T) {
	inline := []*skills.Skill{
		mustInlineSkill(skills.Frontmatter{Name: "enum-inline-a", Description: "Inline A"}, "Instructions A.", nil, nil),
		mustInlineSkill(skills.Frontmatter{Name: "enum-inline-b", Description: "Inline B"}, "Instructions B.", nil, nil),
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: inline})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "enum-inline-a") || !strings.Contains(instructions, "enum-inline-b") {
		t.Fatalf("instructions = %q, want both inline skills", instructions)
	}
}

func TestProvider_CodeDefinedSkillArgument_ProvidesSkill(t *testing.T) {
	skill := &skills.Skill{
		Frontmatter: skills.Frontmatter{Name: "class-a", Description: "Class A"},
		GetContent:  func(context.Context) (string, error) { return "Class instructions.", nil },
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Sources: []skills.Source{skills.NewInMemorySource(skill)}})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "class-a") {
		t.Fatalf("instructions = %q, want code-defined skill", instructions)
	}
}

func TestProvider_CodeDefinedSkillSlice_ProvidesBothSkills(t *testing.T) {
	codeDefined := []*skills.Skill{
		{Frontmatter: skills.Frontmatter{Name: "enum-class-a", Description: "Class A"}, GetContent: func(context.Context) (string, error) { return "Instructions A.", nil }},
		{Frontmatter: skills.Frontmatter{Name: "enum-class-b", Description: "Class B"}, GetContent: func(context.Context) (string, error) { return "Instructions B.", nil }},
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: codeDefined})

	instructions, _ := captureProviderContext(t, provider)
	if !strings.Contains(instructions, "enum-class-a") || !strings.Contains(instructions, "enum-class-b") {
		t.Fatalf("instructions = %q, want both code-defined skills", instructions)
	}
}

func TestProvider_CodeDefinedSkills_DeduplicatesKeepingFirst(t *testing.T) {
	first := &skills.Skill{
		Frontmatter: skills.Frontmatter{Name: "dup-class", Description: "First"},
		GetContent:  func(context.Context) (string, error) { return "First instructions.", nil },
	}
	second := &skills.Skill{
		Frontmatter: skills.Frontmatter{Name: "dup-class", Description: "Second"},
		GetContent:  func(context.Context) (string, error) { return "Second instructions.", nil },
	}
	provider := skills.NewContextProvider(skills.ContextProviderOptions{Skills: []*skills.Skill{first, second}})
	_, tools := captureProviderContext(t, provider)

	got, err := findTool(t, tools, loadSkillToolName).Call(t.Context(), `{"skillName":"dup-class"}`)
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := got.(string); !ok || !strings.Contains(text, "First instructions.") {
		t.Fatalf("load_skill result = %#v, want first code-defined skill", got)
	}
}

func TestProvider_DownloadedSkills_AdvertisesAllTools(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "support-style",
		"---\nname: support-style\ndescription: Contoso Outdoors customer-support tone and formatting guidelines.\n---\n\n# Contoso Outdoors Support Style\n\nYou are speaking on behalf of Contoso Outdoors.\n\n## Canary\n\nInclude STYLE-CANARY-3318.")
	createSkillDirRaw(t, root, "escalation-policy",
		"---\nname: escalation-policy\ndescription: When and how to escalate Contoso Outdoors customer-support tickets.\n---\n\n# Escalation Policy\n\nProvide ESC-CANARY-7742.")
	provider := newProvider(t, root)

	_, options, err := invokeProvider(provider, t.Context(), nil, agent.WithInstructions("You are a customer-support assistant for Contoso Outdoors."))
	if err != nil {
		t.Fatal(err)
	}
	instructions := strings.Join(slices.Collect(agent.AllOptions(options, agent.WithInstructions)), "\n")
	for _, want := range []string{"support-style", "escalation-policy", "Contoso Outdoors customer-support tone"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions = %q, want %q", instructions, want)
		}
	}
	tools := slices.Collect(agent.AllOptions(options, agent.WithTool))
	for _, name := range []string{loadSkillToolName, readSkillResourceToolName, runSkillScriptToolName} {
		findTool(t, tools, name)
	}
}

func TestProvider_LoadDownloadedSkill_ReturnsCanaryAndFrontmatter(t *testing.T) {
	root := t.TempDir()
	createSkillDirRaw(t, root, "support-style",
		"---\nname: support-style\ndescription: Contoso tone guidelines.\n---\n\nInclude STYLE-CANARY-3318 at the bottom.")
	_, tools := captureProviderContext(t, newProvider(t, root))

	got, err := findTool(t, tools, loadSkillToolName).Call(t.Context(), `{"skillName":"support-style"}`)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := got.(string)
	if !ok || !strings.Contains(text, "STYLE-CANARY-3318") || !strings.Contains(text, "name: support-style") {
		t.Fatalf("load_skill result = %#v, want complete downloaded skill", got)
	}
}

func TestProvider_DuplicateFileSkillNames_KeepsOne(t *testing.T) {
	root := t.TempDir()
	createSkillDir(t, root, "dupe", "First", "First body.")
	createSkillDir(t, filepath.Join(root, "subdir"), "dupe", "Second", "Second body.")
	provider := newProvider(t, root)

	instructions, _ := captureProviderContext(t, provider)
	got := advertisedSkills(t, instructions)
	if len(got) != 1 || got[0].Description != "First" && got[0].Description != "Second" {
		t.Fatalf("advertised skills = %#v, want one of the duplicate file skills", got)
	}
}
