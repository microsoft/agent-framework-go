// Copyright (c) Microsoft. All rights reserved.

// Package harness assembles middleware, context providers, and history defaults
// for provider-backed agents.
//
// Create a harness with [New], pass [Harness.Configure]'s result to the normal
// provider constructor, and call [Harness.Close] after its agents finish.
// The harness reuses the agent lifecycle and the provider's tool-invocation
// middleware. Tracing covers agent runs, not individual model calls.
package harness

import (
	"context"
	"fmt"
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
// [agent.Config] passed to [Harness.Configure]. Tool-invocation settings remain
// in the provider constructor's existing configuration.
type Config struct {
	// WorkingDirectory supplies default skills and the agent-file-memory
	// directory. Empty uses the current directory. Relative paths are resolved
	// when New is called.
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

	DisableToolApproval bool
	ToolApproval        toolapproval.Config

	// DisableOpenTelemetry disables agent-run tracing, not feature-usage tracking.
	DisableOpenTelemetry bool
	OpenTelemetry        otelprovider.MiddlewareConfig
}

// Harness owns the default components used by configured agents.
// It is not an agent and does not execute requests itself.
// Keep it open while any configured agent uses its components.
// Configure and Close support concurrent use; callers must finish using
// file memory before closing it.
type Harness struct {
	providers    []agent.ContextProvider
	middlewares  []agent.Middleware
	instructions string
	webSearch    bool
	memory       *filememory.Provider
}

// New creates a harness and opens its default file memory, unless disabled.
// Memory files live under WorkingDirectory/agent-file-memory, with a unique
// persisted working folder per session. Missing memory directories are created.
//
// Close the harness when its configured agents are no longer needed.
// Configuration errors from component constructors retain those constructors'
// panic contracts; filesystem setup failures return an error.
func New(cfg Config) (*Harness, error) {
	h := &Harness{
		instructions: DefaultInstructions,
		webSearch:    !cfg.DisableWebSearch,
	}
	if cfg.Instructions != nil {
		h.instructions = *cfg.Instructions
	}
	h.middlewares = append(h.middlewares, agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			telemetry.MarkUsed(telemetry.FeatureHarnessAgent)
			next(ctx, messages, options...)(yield)
		}
	}))
	if !cfg.DisableToolApproval {
		h.middlewares = append(h.middlewares, toolapproval.New(cfg.ToolApproval))
	}
	if !cfg.DisableOpenTelemetry {
		h.middlewares = append(h.middlewares, otelprovider.NewMiddleware(cfg.OpenTelemetry))
	}
	if !cfg.DisableTodo {
		h.providers = append(h.providers, todo.New(nil))
	}
	if !cfg.DisableAgentMode {
		h.providers = append(h.providers, agentmode.New(cfg.AgentMode))
	}

	skillsConfig := cfg.Skills
	defaultSkills := !cfg.DisableSkills && skillsConfig.Skills == nil && skillsConfig.Sources == nil
	var workingDirectory string
	if !cfg.DisableFileMemory || defaultSkills {
		var err error
		workingDirectory, err = filepath.Abs(cfg.WorkingDirectory)
		if err != nil {
			return nil, fmt.Errorf("harness: resolve working directory: %w", err)
		}
	}
	var skillsProvider agent.ContextProvider
	if !cfg.DisableSkills {
		if defaultSkills {
			skillsConfig.Sources = []skills.Source{
				fsskills.NewSourceOptions(fsskills.SourceOptions{Logger: skillsConfig.Logger}, os.DirFS(workingDirectory)),
			}
		}
		skillsProvider = skills.NewContextProvider(skillsConfig)
	}
	if !cfg.DisableFileMemory {
		memory, err := filememory.New(filepath.Join(workingDirectory, "agent-file-memory"), filememory.Config{
			StateInitializer: func(*agent.Session) filememory.State {
				return filememory.State{WorkingFolder: uuid.NewString()}
			},
		})
		if err != nil {
			return nil, fmt.Errorf("harness: create file memory: %w", err)
		}
		h.memory = memory
		h.providers = append(h.providers, memory)
	}
	if skillsProvider != nil {
		h.providers = append(h.providers, skillsProvider)
	}
	return h, nil
}

// Configure adds harness defaults to cfg without modifying its input slices.
// Call it once for each agent configuration, before the provider constructor.
//
// Existing middleware remains outermost; harness approval and tracing run
// inside it. Built-in context providers precede caller providers. Harness
// instructions precede caller run options. Existing history, message injection,
// and explicit history-conflict settings are retained. Per-service-call history
// persistence is enabled.
//
// Configure adds hosted web search unless disabled or already present in
// cfg.Tools or cfg.RunOptions. It does not add tool-invocation middleware;
// the provider constructor owns that configuration. The caller continues to
// own supplied components.
func (h *Harness) Configure(cfg agent.Config) agent.Config {
	cfg.Middlewares = append(slices.Clone(cfg.Middlewares), h.middlewares...)
	cfg.ContextProviders = append(slices.Clone(h.providers), cfg.ContextProviders...)
	if strings.TrimSpace(h.instructions) != "" {
		cfg.RunOptions = append([]agent.Option{agent.WithInstructions(h.instructions)}, cfg.RunOptions...)
	}
	if h.webSearch {
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
	return cfg
}

// Close releases only file memory created by New. It does not close components
// supplied by callers. It is safe to call more than once.
// Subsequent operations on that file memory report an error wrapping fs.ErrClosed.
func (h *Harness) Close() error {
	if h.memory != nil {
		return h.memory.Close()
	}
	return nil
}
