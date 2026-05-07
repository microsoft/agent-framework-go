// Copyright (c) Microsoft. All rights reserved.

// Package agentmode provides a context provider that tracks the agent's
// operating mode (e.g. "plan" or "execute") in the session state and provides
// tools for querying and switching modes.
//
// This mirrors the .NET AgentModeProvider harness middleware. It enables agents
// to operate in distinct modes during long-running HITL tasks — for example,
// an interactive planning mode vs an autonomous execution mode.
package agentmode

import (
	"context"
	"fmt"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const stateKey = "agentModeState"

const defaultInstructions = `## Agent Mode

You can operate in different modes. Depending on the mode you are in, you will be required to follow different processes.

Use the AgentMode_Get tool to check your current operating mode.
Use the AgentMode_Set tool to switch between modes as your work progresses. Only use AgentMode_Set if the user explicitly instructs/allows you to change modes.

{available_modes}

You are currently operating in the {current_mode} mode.`

// Mode describes a named operating mode with a description of its behavior.
type Mode struct {
	Name        string
	Description string
}

// state is persisted in the session across turns.
type state struct {
	CurrentMode string `json:"currentMode"`
}

// Options configures the agent mode provider.
type Options struct {
	// Modes is the set of available modes. If empty, defaults to "plan" and "execute".
	Modes []Mode

	// DefaultMode is the initial mode. Must be one of the configured Modes.
	// If empty, the first mode is used.
	DefaultMode string

	// Instructions overrides the default instruction template.
	// Use {available_modes} and {current_mode} as placeholders.
	Instructions string
}

var defaultModes = []Mode{
	{
		Name:        "plan",
		Description: "Use this mode when analyzing requirements, breaking down tasks, and creating plans. This is the interactive mode — ask clarifying questions, discuss options, and get user approval before proceeding.",
	},
	{
		Name:        "execute",
		Description: "Use this mode when carrying out approved plans. Work autonomously using your best judgement — do not ask the user questions or wait for feedback. Make reasonable decisions on your own so that there is a complete, useful result when the user returns. If you encounter ambiguity, choose the most reasonable option and note your choice.",
	},
}

// New creates a new agent mode context provider.
// If opts is nil, defaults are used (plan/execute modes).
func New(opts *Options) *agent.ContextProvider {
	modes := defaultModes
	defaultMode := ""
	instructions := defaultInstructions

	if opts != nil {
		if len(opts.Modes) > 0 {
			modes = opts.Modes
		}
		if opts.DefaultMode != "" {
			defaultMode = opts.DefaultMode
		}
		if opts.Instructions != "" {
			instructions = opts.Instructions
		}
	}

	if defaultMode == "" {
		defaultMode = modes[0].Name
	}

	// Validate default mode is in the set.
	validModes := make(map[string]struct{}, len(modes))
	for _, m := range modes {
		validModes[m.Name] = struct{}{}
	}
	if _, ok := validModes[defaultMode]; !ok {
		panic(fmt.Sprintf("agentmode: default mode %q is not in the configured modes list", defaultMode))
	}

	p := &provider{
		modes:        modes,
		defaultMode:  defaultMode,
		instructions: instructions,
		validModes:   validModes,
	}

	return &agent.ContextProvider{
		SourceID: "AgentModeProvider",
		Provide:  p.provide,
	}
}

type provider struct {
	modes        []Mode
	defaultMode  string
	instructions string
	validModes   map[string]struct{}
}

func (p *provider) loadState(opts []agent.Option) *state {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return &state{CurrentMode: p.defaultMode}
	}
	var s state
	if found, _ := session.Get(stateKey, &s); found {
		return &s
	}
	return &state{CurrentMode: p.defaultMode}
}

func (p *provider) saveState(opts []agent.Option, s *state) {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return
	}
	session.Set(stateKey, s)
}

func (p *provider) provide(ctx context.Context, messages []*message.Message, opts ...agent.Option) ([]*message.Message, []agent.Option, error) {
	st := p.loadState(opts)

	tools := p.createTools(opts, st)
	outOpts := make([]agent.Option, len(opts))
	copy(outOpts, opts)
	for _, t := range tools {
		outOpts = append(outOpts, agent.WithTool(t))
	}

	// Build instructions with mode info.
	instructionText := p.buildInstructions(st.CurrentMode)
	instructions := &message.Message{
		Role: message.RoleUser,
		Contents: []message.Content{
			&message.TextContent{Text: instructionText},
		},
	}

	outMessages := make([]*message.Message, 0, len(messages)+1)
	outMessages = append(outMessages, instructions)
	outMessages = append(outMessages, messages...)

	return outMessages, outOpts, nil
}

func (p *provider) buildInstructions(currentMode string) string {
	var sb strings.Builder
	for _, m := range p.modes {
		fmt.Fprintf(&sb, "- \"%s\": %s\n", m.Name, m.Description)
	}
	modesText := sb.String()

	result := strings.ReplaceAll(p.instructions, "{available_modes}", modesText)
	result = strings.ReplaceAll(result, "{current_mode}", currentMode)
	return result
}

func (p *provider) createTools(opts []agent.Option, st *state) []tool.FuncTool {
	modeNames := make([]string, len(p.modes))
	for i, m := range p.modes {
		modeNames[i] = m.Name
	}
	modeNamesDisplay := strings.Join(modeNames, "\", \"")

	setTool := functool.MustNew(
		functool.Config{
			Name:        "AgentMode_Set",
			Description: fmt.Sprintf("Switch the agent's operating mode. Supported modes: \"%s\".", modeNamesDisplay),
		},
		func(ctx tool.Context, mode string) (string, error) {
			if _, ok := p.validModes[mode]; !ok {
				return "", fmt.Errorf("invalid mode: %q. Supported modes: \"%s\"", mode, modeNamesDisplay)
			}
			st.CurrentMode = mode
			p.saveState(opts, st)
			return fmt.Sprintf("Mode changed to %q.", mode), nil
		},
	)

	getTool := functool.MustNew(
		functool.Config{
			Name:        "AgentMode_Get",
			Description: "Get the agent's current operating mode.",
		},
		func(ctx tool.Context, _ struct{}) (string, error) {
			return st.CurrentMode, nil
		},
	)

	return []tool.FuncTool{setTool, getTool}
}

// GetMode returns the current operating mode from the session.
// This can be called externally to read the mode without going through the agent.
func GetMode(opts ...agent.Option) string {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return ""
	}
	var s state
	if found, _ := session.Get(stateKey, &s); found {
		return s.CurrentMode
	}
	return ""
}

// SetMode sets the operating mode in the session.
// This can be called externally to change the mode (e.g. via a /mode command).
func SetMode(mode string, opts ...agent.Option) {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return
	}
	s := state{CurrentMode: mode}
	session.Set(stateKey, s)
}
