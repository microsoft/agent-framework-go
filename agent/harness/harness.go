// Copyright (c) Microsoft. All rights reserved.

// Package harness provides a composed, cross-provider harness configuration
// aligned with the upstream .NET HarnessAgent surface.
package harness

import (
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/agentmode"
	"github.com/microsoft/agent-framework-go/agent/harness/loop"
	"github.com/microsoft/agent-framework-go/agent/harness/todo"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
)

// DefaultInstructions are the built-in harness-level instructions applied when
// Config.DisableInstructions is false and Config.Instructions is empty.
const DefaultInstructions = `You are a helpful AI assistant that uses tools to complete tasks.

## General guidelines

- Think through the task before acting. Break complex work into clear steps.
- Use the tools available to you to gather information, perform actions, and verify results.
- Explain your reasoning and thought process as you work through tasks.
- Explain what you learned and what you are going to do next between tool calls, so the user can follow along with your thought process.
- Avoid making more than 4 tool calls in a row without explaining what you are doing.
- If a tool call fails or returns unexpected results, adapt your approach rather than repeating the same call.
- When you have completed the task, present a clear and concise summary of what you did and what you found.`

// Config configures the composed harness behavior applied by [Configure].
type Config struct {
	// Instructions overrides DefaultInstructions when non-empty.
	Instructions string

	// DisableInstructions omits harness-level instructions entirely.
	DisableInstructions bool

	// DisableTodoProvider omits the default todo tracking context provider.
	DisableTodoProvider bool

	// TodoOptions customizes the default todo provider when it is enabled.
	TodoOptions *todo.Options

	// DisableAgentModeProvider omits the default agent-mode context provider.
	DisableAgentModeProvider bool

	// AgentModeConfig customizes the default agent-mode provider when it is enabled.
	AgentModeConfig agentmode.Config

	// DisableToolApproval omits the default tool-approval middleware.
	DisableToolApproval bool

	// ToolApprovalConfig customizes the default tool-approval middleware when it is enabled.
	ToolApprovalConfig toolapproval.Config

	// LoopConfig enables loop middleware when non-nil and its Evaluators slice is
	// non-empty. Evaluators must be non-nil; loop.New rejects nil evaluators at
	// runtime. The loop middleware is appended before tool-approval middleware so
	// loop reinvocation remains outside approval handling, mirroring the upstream
	// .NET HarnessAgent ordering.
	LoopConfig *loop.Config
}

// Configure returns a copy of cfg with the standard harness instructions,
// context providers, and middlewares applied.
//
// It is intended for composing provider-specific agent configs, for example:
//
//	openaiprovider.AgentConfig{
//		Config: harness.Configure(agent.Config{Name: "Research"}, harness.Config{}),
//		Model:  "gpt-5",
//	}
func Configure(cfg agent.Config, harnessCfg Config) agent.Config {
	out := cfg
	out.RunOptions = slices.Clone(cfg.RunOptions)
	out.ContextProviders = slices.Clone(cfg.ContextProviders)
	out.Middlewares = slices.Clone(cfg.Middlewares)

	if !harnessCfg.DisableInstructions {
		instructions := DefaultInstructions
		if harnessCfg.Instructions != "" {
			instructions = harnessCfg.Instructions
		}
		out.RunOptions = append([]agent.Option{agent.WithInstructions(instructions)}, out.RunOptions...)
	}

	if !harnessCfg.DisableTodoProvider {
		out.ContextProviders = append(out.ContextProviders, todo.New(harnessCfg.TodoOptions))
	}
	if !harnessCfg.DisableAgentModeProvider {
		out.ContextProviders = append(out.ContextProviders, agentmode.New(harnessCfg.AgentModeConfig))
	}
	if harnessCfg.LoopConfig != nil && len(harnessCfg.LoopConfig.Evaluators) > 0 {
		loopCfg := *harnessCfg.LoopConfig
		loopCfg.Evaluators = slices.Clone(loopCfg.Evaluators)
		out.Middlewares = append(out.Middlewares, loop.New(loopCfg))
	}
	if !harnessCfg.DisableToolApproval {
		out.Middlewares = append(out.Middlewares, toolapproval.New(harnessCfg.ToolApprovalConfig))
	}

	return out
}
