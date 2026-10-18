// Copyright (c) Microsoft. All rights reserved.

package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness"
	"github.com/microsoft/agent-framework-go/agent/harness/agentmode"
	"github.com/microsoft/agent-framework-go/agent/harness/filememory"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newHarness(t *testing.T, cfg harness.Config) *harness.Harness {
	t.Helper()
	if cfg.WorkingDirectory == "" {
		cfg.WorkingDirectory = t.TempDir()
	}
	h, err := harness.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}

func disabledConfig() harness.Config {
	return harness.Config{
		DisableTodo:          true,
		DisableAgentMode:     true,
		DisableFileMemory:    true,
		DisableSkills:        true,
		DisableWebSearch:     true,
		DisableToolApproval:  true,
		DisableOpenTelemetry: true,
	}
}

func TestConfigurePreservesCallerConfiguration(t *testing.T) {
	h := newHarness(t, harness.Config{})
	borrowed := &borrowedProvider{ContextProvider: agent.NewContextProvider(agent.ContextProviderConfig{SourceID: "caller"})}
	middleware := &agenttest.Middleware{}
	customTool := agenttest.NewTool("caller-tool", "A caller tool")
	providers := make([]agent.ContextProvider, 8)
	providers[0] = borrowed
	middlewares := make([]agent.Middleware, 8)
	middlewares[0] = middleware
	tools := make([]tool.Tool, 8)
	tools[0] = customTool
	options := make([]agent.Option, 8)
	options[0] = agent.WithInstructions("Caller instructions.")
	history := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	injector := &agent.MessageInjector{}
	input := agent.Config{
		ID:                             "caller-id",
		Name:                           "Caller",
		Description:                    "A configured agent",
		HistoryProvider:                history,
		MessageInjector:                injector,
		WarnOnHistoryProviderConflict:  new(true),
		ThrowOnHistoryProviderConflict: new(true),
		ClearOnHistoryProviderConflict: new(false),
		ContextProviders:               providers[:1],
		Middlewares:                    middlewares[:1],
		Tools:                          tools[:1],
		RunOptions:                     options[:1],
	}
	got := h.Configure(input)
	if got.ID != input.ID || got.Name != input.Name || got.Description != input.Description {
		t.Fatal("agent identity changed")
	}
	if got.HistoryProvider != history || got.MessageInjector != injector {
		t.Fatal("caller history or message injector was replaced")
	}
	if got.WarnOnHistoryProviderConflict != input.WarnOnHistoryProviderConflict ||
		got.ThrowOnHistoryProviderConflict != input.ThrowOnHistoryProviderConflict ||
		got.ClearOnHistoryProviderConflict != input.ClearOnHistoryProviderConflict {
		t.Fatal("explicit history-conflict configuration changed")
	}
	if !got.RequirePerServiceCallHistoryPersistence || input.RequirePerServiceCallHistoryPersistence {
		t.Fatal("per-call history was not enabled on only the returned configuration")
	}
	if len(got.ContextProviders) != 5 || got.ContextProviders[4] != borrowed {
		t.Fatal("caller context provider must follow the four built-ins")
	}
	if len(got.Middlewares) != 4 || got.Middlewares[0] != middleware {
		t.Fatal("caller middleware must remain outside approval and tracing")
	}
	if len(got.Tools) != 2 || got.Tools[0] != customTool {
		t.Fatal("caller tools were not retained before hosted web search")
	}
	if _, ok := got.Tools[1].(*hostedtool.WebSearch); !ok {
		t.Fatalf("default hosted tool = %T", got.Tools[1])
	}
	wantInstructions := []string{harness.DefaultInstructions, "Caller instructions."}
	if got := slices.Collect(agent.AllOptions(got.RunOptions, agent.WithInstructions)); !slices.Equal(got, wantInstructions) {
		t.Fatalf("instructions = %q, want %q", got, wantInstructions)
	}
	for i := 1; i < len(providers); i++ {
		if providers[i] != nil || middlewares[i] != nil || tools[i] != nil || options[i] != nil {
			t.Fatalf("caller backing arrays changed at index %d", i)
		}
	}
	if len(input.ContextProviders) != 1 || len(input.Middlewares) != 1 || len(input.Tools) != 1 || len(input.RunOptions) != 1 {
		t.Fatal("input slice lengths changed")
	}
	second := h.Configure(agent.Config{})
	if len(second.ContextProviders) != 4 || len(second.Middlewares) != 3 || len(second.Tools) != 1 {
		t.Fatal("one configured agent changed the defaults for another")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if borrowed.closed {
		t.Fatal("harness closed a caller-supplied context provider")
	}
}

func TestInstructions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value *string
		want  []string
	}{
		{name: "default", want: []string{harness.DefaultInstructions}},
		{name: "custom", value: new("Harness guidance."), want: []string{"Harness guidance."}},
		{name: "empty", value: new("")},
		{name: "whitespace", value: new(" \t\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := disabledConfig()
			settings.Instructions = tc.value
			h := newHarness(t, settings)
			cfg := h.Configure(agent.Config{})
			if got := slices.Collect(agent.AllOptions(cfg.RunOptions, agent.WithInstructions)); !slices.Equal(got, tc.want) {
				t.Fatalf("instructions = %q, want %q", got, tc.want)
			}
			cfg = h.Configure(agent.Config{RunOptions: []agent.Option{agent.WithInstructions("Caller.")}})
			want := append(slices.Clone(tc.want), "Caller.")
			if got := slices.Collect(agent.AllOptions(cfg.RunOptions, agent.WithInstructions)); !slices.Equal(got, want) {
				t.Fatalf("instructions with caller = %q, want %q", got, want)
			}
		})
	}
}

func TestComponentOptOuts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		settings    harness.Config
		providers   int
		middlewares int
		tools       int
	}{
		{name: "defaults", providers: 4, middlewares: 3, tools: 1},
		{name: "todo", settings: harness.Config{DisableTodo: true}, providers: 3, middlewares: 3, tools: 1},
		{name: "mode", settings: harness.Config{DisableAgentMode: true}, providers: 3, middlewares: 3, tools: 1},
		{name: "memory", settings: harness.Config{DisableFileMemory: true}, providers: 3, middlewares: 3, tools: 1},
		{name: "skills", settings: harness.Config{DisableSkills: true}, providers: 3, middlewares: 3, tools: 1},
		{name: "web search", settings: harness.Config{DisableWebSearch: true}, providers: 4, middlewares: 3},
		{name: "approval", settings: harness.Config{DisableToolApproval: true}, providers: 4, middlewares: 2, tools: 1},
		{name: "tracing", settings: harness.Config{DisableOpenTelemetry: true}, providers: 4, middlewares: 2, tools: 1},
		{name: "all", settings: disabledConfig(), middlewares: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.settings)
			cfg := h.Configure(agent.Config{})
			if len(cfg.ContextProviders) != tc.providers || len(cfg.Middlewares) != tc.middlewares || len(cfg.Tools) != tc.tools {
				t.Fatalf("got %d providers, %d middleware, %d tools; want %d, %d, %d",
					len(cfg.ContextProviders), len(cfg.Middlewares), len(cfg.Tools), tc.providers, tc.middlewares, tc.tools)
			}
			if cfg.HistoryProvider == nil || cfg.MessageInjector == nil || !cfg.RequirePerServiceCallHistoryPersistence {
				t.Fatal("history or message-injection defaults are missing")
			}
			if cfg.WarnOnHistoryProviderConflict == nil || *cfg.WarnOnHistoryProviderConflict ||
				cfg.ThrowOnHistoryProviderConflict == nil || *cfg.ThrowOnHistoryProviderConflict {
				t.Fatal("default history-conflict settings are missing")
			}
		})
	}
}

func TestConfigureRetainsExistingWebSearch(t *testing.T) {
	webSearch := &hostedtool.WebSearch{AdditionalProperties: map[string]any{
		"user_location": map[string]string{"country": "US"},
	}}
	other := agenttest.NewTool("other", "Another caller tool")
	for _, tc := range []struct {
		name      string
		config    agent.Config
		wantTools int
	}{
		{name: "tools", config: agent.Config{Tools: []tool.Tool{webSearch}}, wantTools: 1},
		{name: "run options", config: agent.Config{RunOptions: []agent.Option{agent.WithTool(webSearch)}}, wantTools: 1},
		{name: "run options before another tool", config: agent.Config{
			RunOptions: []agent.Option{agent.WithTool(webSearch), agent.WithTool(other)},
		}, wantTools: 2},
		{name: "run options with configured tools", config: agent.Config{
			Tools:      []tool.Tool{other},
			RunOptions: []agent.Option{agent.WithTool(webSearch)},
		}, wantTools: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, harness.Config{})
			cfg := h.Configure(tc.config)
			tools := append(slices.Collect(agent.AllOptions(cfg.RunOptions, agent.WithTool)), cfg.Tools...)
			var count int
			var search *hostedtool.WebSearch
			for _, candidate := range tools {
				if found, ok := candidate.(*hostedtool.WebSearch); ok {
					count++
					search = found
				}
			}
			if len(tools) != tc.wantTools || count != 1 || search != webSearch {
				t.Fatalf("got %d tools and %d web searches; want %d tools with the original search", len(tools), count, tc.wantTools)
			}
		})
	}
}

func TestDefaultToolsAndFileSkills(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			root := t.TempDir()
			skillDirectory := filepath.Join(root, "skills", "composition-skill")
			if err := os.MkdirAll(skillDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			skill := "---\nname: composition-skill\ndescription: Verify the harness composition.\n---\nUse the existing harness components."
			if err := os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"), []byte(skill), 0o600); err != nil {
				t.Fatal(err)
			}
			h := newHarness(t, harness.Config{WorkingDirectory: root})
			runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, _ []*message.Message, opts ...agent.Option) {
				var names []string
				for fn := range agent.AllOptions(opts, agent.WithTool) {
					names = append(names, fn.Name())
				}
				want := []string{
					"todos_add", "todos_complete", "todos_remove", "todos_get_remaining", "todos_get_all",
					"mode_set", "mode_get",
					"file_memory_write", "file_memory_read", "file_memory_delete", "file_memory_ls",
					"file_memory_grep", "file_memory_replace", "file_memory_replace_lines",
					"load_skill", "read_skill_resource", "run_skill_script", "web_search",
				}
				slices.Sort(names)
				slices.Sort(want)
				if !slices.Equal(names, want) {
					t.Fatalf("default tools = %v, want %v", names, want)
				}
				instructions := slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
				if len(instructions) < 2 || instructions[0] != harness.DefaultInstructions || instructions[1] != "Caller instructions." {
					t.Fatalf("instruction order = %q", instructions)
				}
				if !strings.Contains(strings.Join(instructions, "\n"), "composition-skill") {
					t.Fatal("file-based skill was not discovered")
				}
			}).
				AddFunctionCall("write", filememory.WriteToolName, `{"fileName":"project.md","content":"Use Go.","description":"Project preferences"}`).
				AddFunctionCall("todo", "todos_add", `{"Arg0":[{"title":"Review the endpoint design"}]}`).
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					assertToolHistory(t, messages, "write", "todo")
					for _, msg := range messages {
						for _, content := range msg.Contents {
							if result, ok := content.(*message.FunctionResultContent); ok && result.Error != nil {
								t.Errorf("tool %s failed: %v", result.CallID, result.Error)
							}
						}
					}
				}).
				AddText("saved").
				Build()}
			a := newLocalAgent(h.Configure(agent.Config{
				RunOptions: []agent.Option{agent.WithInstructions("Caller instructions.")},
			}), runner.Run)
			response, err := a.RunText(t.Context(), "Remember our Go project.",
				agent.WithSession(&agent.Session{}), agent.Stream(stream)).Collect()
			if err != nil || response.String() != "saved" {
				t.Fatalf("response = %v, %v, want saved", response, err)
			}
		})
	}
}

func TestConfiguredModeAndSkills(t *testing.T) {
	h := newHarness(t, harness.Config{
		DisableFileMemory: true,
		AgentMode:         agentmode.Config{DefaultMode: new("execute")},
		Skills: skills.ContextProviderOptions{Skills: []*skills.Skill{{
			Frontmatter: skills.Frontmatter{Name: "inline-skill", Description: "Caller-supplied skill"},
			GetContent:  func(context.Context) (string, error) { return "Skill guidance.", nil },
		}}},
	})
	cfg := h.Configure(agent.Config{})
	mode, ok := cfg.ContextProviders[1].(*agentmode.Provider)
	if !ok || mode.Mode(&agent.Session{}) != "execute" {
		t.Fatal("custom mode configuration was not retained")
	}
	_, opts, err := cfg.ContextProviders[2].Invoking(t.Context(), agent.InvokingContext{})
	if err != nil {
		t.Fatal(err)
	}
	instructions := strings.Join(slices.Collect(agent.AllOptions(opts, agent.WithInstructions)), "\n")
	if !strings.Contains(instructions, "inline-skill") {
		t.Fatalf("custom skills were not retained: %s", instructions)
	}
}

func TestCloseOwnsOnlyCreatedMemory(t *testing.T) {
	h := newHarness(t, harness.Config{})
	cfg := h.Configure(agent.Config{})
	for range 2 {
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := cfg.ContextProviders[2].Invoking(t.Context(), agent.InvokingContext{
		Options: []agent.Option{agent.WithSession(&agent.Session{})},
	})
	if !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("memory after Close = %v, want fs.ErrClosed", err)
	}
}

func TestDisabledFilesystemComponentsDoNotCreateDirectories(t *testing.T) {
	cfg := disabledConfig()
	cfg.WorkingDirectory = filepath.Join(t.TempDir(), "not-created")
	h := newHarness(t, cfg)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.WorkingDirectory); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("disabled filesystem components created a working directory: %v", err)
	}
}

func TestNewReportsFilesystemFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h, err := harness.New(harness.Config{WorkingDirectory: file}); h != nil || err == nil {
		t.Fatalf("New with file as working directory = %v, %v", h, err)
	}
}

func TestApprovalAndResume(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, approve := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/approve=%v", stream, approve), func(t *testing.T) {
				h := newHarness(t, harness.Config{})
				var executions int
				fn := functool.MustNew(functool.Config{Name: "test_action"}, func(context.Context, struct{}) (string, error) {
					executions++
					return "completed", nil
				})
				runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().
					AddFunctionCall("protected", fn.Name(), `{}`).
					NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
						assertToolHistory(t, messages, "protected")
						for _, msg := range messages {
							for _, content := range msg.Contents {
								if result, ok := content.(*message.FunctionResultContent); ok && result.CallID == "protected" {
									if approve && (result.Error != nil || result.Result != "completed") {
										t.Errorf("approved result = %v, %v", result.Result, result.Error)
									}
									if !approve && result.Result == "completed" {
										t.Error("rejected action produced a successful result")
									}
								}
							}
						}
					}).
					AddText("finished").
					Build()}
				a := newLocalAgent(h.Configure(agent.Config{
					Tools: []tool.Tool{tool.ApprovalRequiredFunc(fn)},
				}), runner.Run)
				session := &agent.Session{}
				response, err := a.RunText(t.Context(), "Run the test action.",
					agent.WithSession(session), agent.Stream(stream)).Collect()
				if err != nil {
					t.Fatal(err)
				}
				var request *message.ToolApprovalRequestContent
				for content := range response.Contents() {
					if approval, ok := content.(*message.ToolApprovalRequestContent); ok {
						if request != nil {
							t.Fatal("one tool call produced multiple approval requests")
						}
						request = approval
					}
				}
				if request == nil || executions != 0 {
					t.Fatalf("before approval: request = %v, executions = %d", request, executions)
				}
				data, err := json.Marshal(session)
				if err != nil {
					t.Fatal(err)
				}
				var restored agent.Session
				if err := json.Unmarshal(data, &restored); err != nil {
					t.Fatal(err)
				}
				response, err = a.RunMessage(t.Context(), message.New(request.CreateResponse(approve, "test decision")),
					agent.WithSession(&restored), agent.Stream(stream)).Collect()
				if err != nil || response.String() != "finished" {
					t.Fatalf("resumed response = %v, %v", response, err)
				}
				wantExecutions := 0
				if approve {
					wantExecutions = 1
				}
				if executions != wantExecutions {
					t.Fatalf("executions = %d, want %d", executions, wantExecutions)
				}
				assertToolHistory(t, sessionHistory(t, &restored), "protected")
			})
		}
	}
}

func TestPerCallHistoryIsStoredOnce(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			h := newHarness(t, harness.Config{})
			session := &agent.Session{}
			var executions int
			fn := functool.MustNew(functool.Config{Name: "lookup"}, func(context.Context, struct{}) (string, error) {
				executions++
				var calls int
				for _, msg := range sessionHistory(t, session) {
					for _, content := range msg.Contents {
						if _, ok := content.(*message.FunctionCallContent); ok {
							calls++
						}
					}
				}
				if calls != executions {
					t.Errorf("tool executed before model call was stored: %d calls, %d executions", calls, executions)
				}
				return "found", nil
			})
			runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().
				AddFunctionCall("first", fn.Name(), `{}`).
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					assertToolHistory(t, messages, "first")
					assertTextCount(t, messages, "Start the lookup.", 1)
				}).
				AddFunctionCall("second", fn.Name(), `{}`).
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					assertToolHistory(t, messages, "first", "second")
					assertTextCount(t, messages, "Start the lookup.", 1)
				}).
				AddText("done").
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					assertToolHistory(t, messages, "first", "second")
					for _, text := range []string{"Start the lookup.", "done", "Next request."} {
						assertTextCount(t, messages, text, 1)
					}
				}).
				AddText("next done").
				Build()}
			a := newLocalAgent(h.Configure(agent.Config{Tools: []tool.Tool{fn}}), runner.Run)
			for _, prompt := range []string{"Start the lookup.", "Next request."} {
				if _, err := a.RunText(t.Context(), prompt, agent.WithSession(session), agent.Stream(stream)).Collect(); err != nil {
					t.Fatal(err)
				}
			}
			history := sessionHistory(t, session)
			assertToolHistory(t, history, "first", "second")
			for _, text := range []string{"Start the lookup.", "done", "Next request.", "next done"} {
				assertTextCount(t, history, text, 1)
			}
			if executions != 2 {
				t.Fatalf("tool executed %d times, want 2", executions)
			}
		})
	}
}

func TestRestoredSessionKeepsMemoryAfterHarnessReopens(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			root := t.TempDir()
			h := newHarness(t, harness.Config{WorkingDirectory: root})
			cfg := h.Configure(agent.Config{})
			memory := cfg.ContextProviders[2].(*filememory.Provider)
			runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder().
				AddFunctionCall("write", filememory.WriteToolName, `{"fileName":"project.md","content":"Use Go.","description":"Project preferences"}`).
				NewTurn().
				AddText("saved").
				Build()}
			a := newLocalAgent(cfg, runner.Run)
			session := &agent.Session{}
			if _, err := a.RunText(t.Context(), "Remember this project.", agent.WithSession(session), agent.Stream(stream)).Collect(); err != nil {
				t.Fatal(err)
			}
			var before filememory.State
			if found, err := session.Get(memory.StateKeys()[0], &before); err != nil || !found || before.WorkingFolder == "" {
				t.Fatalf("memory state = %+v, found = %v, error = %v", before, found, err)
			}
			data, err := json.Marshal(session)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			var restored agent.Session
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			reopened := newHarness(t, harness.Config{WorkingDirectory: root})
			runner = &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
				var hasIndex bool
				for _, msg := range messages {
					hasIndex = hasIndex || msg.Source.ID == "FileMemoryProvider" && strings.Contains(msg.Contents.Text(), "- **project.md**: Project preferences")
				}
				if !hasIndex {
					t.Fatal("restored session did not receive its memory index")
				}
				assertTextCount(t, messages, "Remember this project.", 1)
				assertTextCount(t, messages, "saved", 1)
			}).
				AddFunctionCall("read", filememory.ReadFileToolName, `{"fileName":"project.md"}`).
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					assertToolHistory(t, messages, "write", "read")
					for _, msg := range messages {
						for _, content := range msg.Contents {
							if result, ok := content.(*message.FunctionResultContent); ok && result.CallID == "read" {
								if result.Error != nil || result.Result != "Use Go." {
									t.Errorf("restored memory read = %v, %v", result.Result, result.Error)
								}
							}
						}
					}
				}).
				AddText("recalled").
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					assertTextCount(t, messages, "Remember this project.", 0)
					for _, msg := range messages {
						if msg.Source.ID == "FileMemoryProvider" {
							t.Fatal("new session received the restored session's memory index")
						}
					}
				}).
				AddFunctionCall("list", filememory.LsToolName, `{}`).
				NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
					assertToolHistory(t, messages, "list")
					for _, msg := range messages {
						for _, content := range msg.Contents {
							if result, ok := content.(*message.FunctionResultContent); ok && result.CallID == "list" {
								data, err := json.Marshal(result.Result)
								if result.Error != nil || err != nil || string(data) != "[]" {
									t.Errorf("new-session listing = %s, tool error = %v, JSON error = %v", data, result.Error, err)
								}
							}
						}
					}
				}).
				AddText("new session").
				Build()}
			a = newLocalAgent(reopened.Configure(agent.Config{}), runner.Run)
			response, err := a.RunText(t.Context(), "Recall the project.", agent.WithSession(&restored), agent.Stream(stream)).Collect()
			if err != nil || response.String() != "recalled" {
				t.Fatalf("restored response = %v, %v", response, err)
			}
			var after filememory.State
			if found, err := restored.Get(memory.StateKeys()[0], &after); err != nil || !found || after != before {
				t.Fatalf("restored memory state = %+v, want %+v; found = %v, error = %v", after, before, found, err)
			}
			fresh := &agent.Session{}
			if _, err := a.RunText(t.Context(), "Start fresh.", agent.WithSession(fresh), agent.Stream(stream)).Collect(); err != nil {
				t.Fatal(err)
			}
			var freshState filememory.State
			if found, err := fresh.Get(memory.StateKeys()[0], &freshState); err != nil || !found || freshState.WorkingFolder == "" || freshState == before {
				t.Fatalf("new-session memory state = %+v, found = %v, error = %v", freshState, found, err)
			}
		})
	}
}

func TestCancellationReleasesRunWithoutClosingComponents(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, during := range []string{"provider", "tool"} {
			t.Run(fmt.Sprintf("stream=%v/during=%s", stream, during), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					h := newHarness(t, harness.Config{})
					borrowed := &borrowedProvider{ContextProvider: agent.NewContextProvider(agent.ContextProviderConfig{SourceID: "caller"})}
					entered := make(chan struct{})
					fn := functool.MustNew(functool.Config{Name: "wait_for_cancellation"}, func(ctx context.Context, _ struct{}) (string, error) {
						close(entered)
						<-ctx.Done()
						return "", ctx.Err()
					})
					var calls, closed int
					run := func(ctx context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
						return func(yield func(*agent.ResponseUpdate, error) bool) {
							calls++
							defer func() { closed++ }()
							if calls == 1 {
								if during == "provider" {
									close(entered)
									<-ctx.Done()
									yield(nil, ctx.Err())
								} else {
									yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{
										&message.FunctionCallContent{CallID: "wait", Name: fn.Name(), Arguments: `{}`},
									}}, nil)
								}
								return
							}
							yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "ready"}}}, nil)
						}
					}
					a := newLocalAgent(h.Configure(agent.Config{
						Tools:            []tool.Tool{fn},
						ContextProviders: []agent.ContextProvider{borrowed},
					}), run)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					finished := make(chan error, 1)
					go func() {
						_, err := a.RunText(ctx, "Wait for cancellation.", agent.WithSession(&agent.Session{}), agent.Stream(stream)).Collect()
						finished <- err
					}()
					<-entered
					cancel()
					if err := <-finished; !errors.Is(err, context.Canceled) {
						t.Fatalf("cancelled run = %v, want context.Canceled", err)
					}
					if calls != 1 || closed != 1 || borrowed.closed {
						t.Fatalf("after cancellation: provider calls = %d, closed = %d, caller component closed = %v", calls, closed, borrowed.closed)
					}
					response, err := a.RunText(t.Context(), "Try a new session.", agent.WithSession(&agent.Session{}), agent.Stream(stream)).Collect()
					if err != nil || response.String() != "ready" {
						t.Fatalf("run after cancellation = %v, %v", response, err)
					}
					if err := h.Close(); err != nil {
						t.Fatal(err)
					}
					if calls != 2 || closed != 2 || borrowed.closed {
						t.Fatalf("after cleanup: calls = %d, closed = %d, caller component closed = %v", calls, closed, borrowed.closed)
					}
				})
			})
		}
	}
}

func TestEarlyStreamStopReleasesProviderAndTracing(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(tracerProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(originalProvider)
		if err := tracerProvider.Shutdown(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	h := newHarness(t, harness.Config{})
	borrowed := &borrowedProvider{ContextProvider: agent.NewContextProvider(agent.ContextProviderConfig{SourceID: "caller"})}
	var closed bool
	a := newLocalAgent(h.Configure(agent.Config{
		Name:             "HarnessTest",
		ContextProviders: []agent.ContextProvider{borrowed},
	}), func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			defer func() { closed = true }()
			if !yield(&agent.ResponseUpdate{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "partial"}}}, nil) {
				return
			}
			t.Error("provider continued after the caller stopped the stream")
		}
	})
	session := &agent.Session{}
	var text string
	for update, err := range a.RunText(t.Context(), "Stream a response.", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		if update != nil && update.String() != "" {
			text = update.String()
			break
		}
	}
	if text != "partial" || !closed || borrowed.closed {
		t.Fatalf("early stop: text = %q, provider closed = %v, caller component closed = %v", text, closed, borrowed.closed)
	}
	history := sessionHistory(t, session)
	assertTextCount(t, history, "Stream a response.", 1)
	assertTextCount(t, history, "partial", 0)
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "invoke_agent HarnessTest" || spans[0].EndTime.IsZero() {
		t.Fatalf("early-stop spans = %+v, want one completed agent span", spans)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if borrowed.closed {
		t.Fatal("cleanup closed a caller-supplied component")
	}
}

func newLocalAgent(cfg agent.Config, run agent.RunFunc) *agent.Agent {
	return agent.New(agent.ProviderConfig{
		ProviderName: "test",
		Run:          run,
		Middlewares:  []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	}, cfg)
}

func sessionHistory(t *testing.T, session *agent.Session) []*message.Message {
	t.Helper()
	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	return history
}

func assertTextCount(t *testing.T, messages []*message.Message, text string, want int) {
	t.Helper()
	var count int
	for _, msg := range messages {
		if msg.Contents.Text() == text {
			count++
		}
	}
	if count != want {
		t.Errorf("text %q appears %d times, want %d", text, count, want)
	}
}

func assertToolHistory(t *testing.T, messages []*message.Message, ids ...string) {
	t.Helper()
	calls, results := make(map[string]int), make(map[string]int)
	for _, msg := range messages {
		for _, content := range msg.Contents {
			switch content := content.(type) {
			case *message.FunctionCallContent:
				calls[content.CallID]++
			case *message.FunctionResultContent:
				results[content.CallID]++
			}
		}
	}
	if len(calls) != len(ids) || len(results) != len(ids) {
		t.Fatalf("tool calls = %v, results = %v, want IDs %v", calls, results, ids)
	}
	for _, id := range ids {
		if calls[id] != 1 || results[id] != 1 {
			t.Errorf("tool %s has %d calls and %d results, want one each", id, calls[id], results[id])
		}
	}
}

type borrowedProvider struct {
	agent.ContextProvider
	closed bool
}

func (p *borrowedProvider) Close() error {
	p.closed = true
	return nil
}
