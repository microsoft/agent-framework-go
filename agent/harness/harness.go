// Copyright (c) Microsoft. All rights reserved.

// Package harness assembles middleware, context providers, and history defaults
// for provider-backed agents.
//
// Pass [AgentConfig]'s result to the normal provider constructor.
// The harness reuses the agent lifecycle and the provider's tool-invocation
// middleware. Tracing covers agent runs, function tools, and model calls made by
// supported model adapters.
package harness

import (
	"context"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/agentmode"
	"github.com/microsoft/agent-framework-go/agent/harness/filememory"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/fsskills"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/otelprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
)

// DefaultInstructions guides tool use and progress updates.
const DefaultInstructions = `You are a helpful AI assistant that uses tools to complete tasks.

## General guidelines

- Think through the task before acting. Break complex work into clear steps.
- Use the tools available to gather information, perform actions, and verify results.
- Give brief explanations of your actions and progress between tool calls.
- Avoid making more than four tool calls in a row without a progress update.
- If a tool fails or returns unexpected results, adapt instead of repeating the same call.
- Finish with a clear, concise summary of what you did and found.`

// Config selects harness components. Its zero value enables the defaults.
// Agent identity, history, additional tools and providers belong in the
// [agent.Config] passed to [AgentConfig]. Tool-invocation settings remain
// in the provider constructor's existing configuration.
type Config struct {
	// WorkingDirectory supplies default skills and the agent-file-memory
	// directory. Empty uses the current directory. Relative paths are resolved
	// when AgentConfig is called.
	WorkingDirectory string

	// Instructions replaces DefaultInstructions. Nil uses the defaults.
	// An empty or whitespace-only value omits harness instructions.
	Instructions *string

	DisableTodo       bool
	DisableAgentMode  bool
	DisableFileMemory bool
	DisableSkills     bool
	DisableWebSearch  bool

	// AgentMode configures the mode provider unless DisableAgentMode is true.
	AgentMode agentmode.Config

	// Skills configures the skills provider unless DisableSkills is true.
	// When both Skills.Skills and Skills.Sources are nil, skills are discovered
	// from WorkingDirectory. Non-nil collections replace that default source.
	Skills skills.ContextProviderOptions

	// DisableToolApproval omits standing-approval and auto-approval middleware.
	// It does not remove the provider's approval requirements from tools.
	DisableToolApproval bool
	ToolApproval        toolapproval.Config

	// DisableOpenTelemetry disables agent, tool, and model-call tracing, not
	// feature-usage tracking. When tracing is enabled, OpenTelemetry can
	// independently disable model-call instrumentation.
	DisableOpenTelemetry bool
	OpenTelemetry        otelprovider.MiddlewareConfig
}

const memoryDirectory = "agent-file-memory"

// AgentConfig adds harness defaults to cfg without modifying its input slices.
// Call it once for each agent configuration, before the provider constructor.
//
// Caller middleware remains outermost; built-in context providers precede
// caller providers. Harness instructions precede caller run options. Existing
// history, message injection, tools and explicit history-conflict settings are
// retained. Per-service-call history persistence is enabled.
//
// File memory uses a unique persisted working folder per session below
// WorkingDirectory/agent-file-memory. That subtree is excluded from default
// skill discovery and resource access. Its handles are cleaned up automatically
// when the provider is no longer reachable; [filememory.Provider.Close] can
// release them earlier.
//
// Provider constructors remain responsible for tool-invocation configuration.
// Configuration errors from component constructors retain those constructors'
// panic contracts; filesystem setup failures return an error.
func AgentConfig(settings Config, cfg agent.Config) (agent.Config, error) {
	middlewares := slices.Clone(cfg.Middlewares)
	middlewares = append(middlewares, agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			telemetry.MarkUsed(telemetry.FeatureHarnessAgent)
			next(ctx, messages, options...)(yield)
		}
	}))
	if !settings.DisableToolApproval {
		middlewares = append(middlewares, toolapproval.New(settings.ToolApproval))
	}
	if !settings.DisableOpenTelemetry {
		middlewares = append(middlewares, otelprovider.NewMiddleware(settings.OpenTelemetry))
	}
	providers := make([]agent.ContextProvider, 0, 4+len(cfg.ContextProviders))
	if !settings.DisableTodo {
		providers = append(providers, todo.New(nil))
	}
	if !settings.DisableAgentMode {
		providers = append(providers, agentmode.New(settings.AgentMode))
	}

	skillsConfig := settings.Skills
	defaultSkills := !settings.DisableSkills && skillsConfig.Skills == nil && skillsConfig.Sources == nil
	var workingDirectory string
	if !settings.DisableFileMemory || defaultSkills {
		var err error
		workingDirectory, err = filepath.Abs(settings.WorkingDirectory)
		if err != nil {
			return agent.Config{}, fmt.Errorf("harness: resolve working directory: %w", err)
		}
	}
	var skillsProvider agent.ContextProvider
	if !settings.DisableSkills {
		if defaultSkills {
			skillsConfig.Sources = []skills.Source{
				fsskills.NewSourceOptions(fsskills.SourceOptions{Logger: skillsConfig.Logger}, defaultSkillsFS(workingDirectory)),
			}
		}
		skillsProvider = skills.NewContextProvider(skillsConfig)
	}
	if !settings.DisableFileMemory {
		memory, err := filememory.New(filepath.Join(workingDirectory, memoryDirectory), filememory.Config{
			StateInitializer: func(*agent.Session) filememory.State {
				return filememory.State{WorkingFolder: uuid.NewString()}
			},
		})
		if err != nil {
			return agent.Config{}, fmt.Errorf("harness: create file memory: %w", err)
		}
		providers = append(providers, memory)
	}
	if skillsProvider != nil {
		providers = append(providers, skillsProvider)
	}
	cfg.Middlewares = middlewares
	cfg.ContextProviders = append(providers, cfg.ContextProviders...)
	instructions := DefaultInstructions
	if settings.Instructions != nil {
		instructions = *settings.Instructions
	}
	if strings.TrimSpace(instructions) != "" {
		cfg.RunOptions = append([]agent.Option{agent.WithInstructions(instructions)}, cfg.RunOptions...)
	}
	if !settings.DisableWebSearch {
		hasWebSearch := slices.ContainsFunc(cfg.Tools, func(t tool.Tool) bool {
			_, ok := t.(*hostedtool.WebSearch)
			return ok
		})
		if !hasWebSearch {
			for t := range agent.AllOptions(cfg.RunOptions, agent.WithTool) {
				if _, ok := t.(*hostedtool.WebSearch); ok {
					hasWebSearch = true
					break
				}
			}
		}
		if !hasWebSearch {
			cfg.Tools = append(slices.Clone(cfg.Tools), &hostedtool.WebSearch{})
		}
	}
	if cfg.HistoryProvider == nil {
		cfg.HistoryProvider = agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
	}
	if cfg.MessageInjector == nil {
		cfg.MessageInjector = &agent.MessageInjector{}
	}
	if cfg.WarnOnHistoryProviderConflict == nil {
		cfg.WarnOnHistoryProviderConflict = new(false)
	}
	if cfg.ThrowOnHistoryProviderConflict == nil {
		cfg.ThrowOnHistoryProviderConflict = new(false)
	}
	cfg.RequirePerServiceCallHistoryPersistence = true
	return cfg, nil
}

type defaultSkillsFS string

func checkSkillPath(name, op string) error {
	first, _, _ := strings.Cut(name, "/")
	if !fs.ValidPath(name) || strings.EqualFold(first, memoryDirectory) {
		return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	}
	return nil
}

func (f defaultSkillsFS) Open(name string) (fs.File, error) {
	if err := checkSkillPath(name, "open"); err != nil {
		return nil, err
	}
	return os.DirFS(string(f)).Open(name)
}

func (f defaultSkillsFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := checkSkillPath(name, "readdir"); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(os.DirFS(string(f)), name)
	if name == "." {
		entries = slices.DeleteFunc(entries, func(entry fs.DirEntry) bool {
			return strings.EqualFold(entry.Name(), memoryDirectory)
		})
	}
	return entries, err
}

func (f defaultSkillsFS) Lstat(name string) (fs.FileInfo, error) {
	if err := checkSkillPath(name, "lstat"); err != nil {
		return nil, err
	}
	return fs.Lstat(os.DirFS(string(f)), name)
}

func (f defaultSkillsFS) ReadLink(name string) (string, error) {
	if err := checkSkillPath(name, "readlink"); err != nil {
		return "", err
	}
	return fs.ReadLink(os.DirFS(string(f)), name)
}
