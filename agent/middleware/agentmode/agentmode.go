// Copyright (c) Microsoft. All rights reserved.

// Package agentmode provides a context provider that tracks the agent's
// operating mode (e.g. "plan" or "execute") in the session state and provides
// tools for querying and switching modes.
//
// It enables agents to operate in distinct modes during long-running HITL
// tasks — for example, an interactive planning mode vs an autonomous
// execution mode.
package agentmode

import (
	"context"
	"fmt"
	"slices"
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
	CurrentMode  string `json:"currentMode"`
	PreviousMode string `json:"previousMode,omitempty"`
}

// Config configures the agent mode provider.
type Config struct {
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
// A zero-value Config uses defaults (plan/execute modes).
//
// Panics if the configuration is invalid (empty modes, duplicate names,
// empty mode name, or default mode not in the configured set).
func New(cfg Config) *Provider {
	modes := defaultModes
	defaultMode := ""
	instructions := defaultInstructions

	if len(cfg.Modes) > 0 {
		modes = cfg.Modes
	}
	if cfg.DefaultMode != "" {
		defaultMode = cfg.DefaultMode
	}
	if cfg.Instructions != "" {
		instructions = cfg.Instructions
	}

	if len(modes) == 0 {
		panic("agentmode: at least one mode must be configured")
	}

	if defaultMode == "" {
		defaultMode = modes[0].Name
	}

	// Validate modes: no empty names, no duplicates.
	validModes := make(map[string]struct{}, len(modes))
	for i, m := range modes {
		if strings.TrimSpace(m.Name) == "" {
			panic(fmt.Sprintf("agentmode: mode at index %d has an empty name", i))
		}
		if _, exists := validModes[m.Name]; exists {
			panic(fmt.Sprintf("agentmode: duplicate mode name %q", m.Name))
		}
		validModes[m.Name] = struct{}{}
	}
	if _, ok := validModes[defaultMode]; !ok {
		panic(fmt.Sprintf("agentmode: default mode %q is not in the configured modes list", defaultMode))
	}

	p := &Provider{
		modes:        modes,
		defaultMode:  defaultMode,
		instructions: instructions,
		validModes:   validModes,
	}

	p.ContextProvider = agent.ContextProvider{
		SourceID: "AgentModeProvider",
		Provide:  p.provide,
	}
	return p
}

// Provider is an agent mode context provider.
// Use [New] to create. The embedded [agent.ContextProvider] can be used
// directly in agent configuration.
type Provider struct {
	agent.ContextProvider
	modes        []Mode
	defaultMode  string
	instructions string
	validModes   map[string]struct{}
}

func (p *Provider) loadState(opts []agent.Option) *state {
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

func (p *Provider) saveState(opts []agent.Option, s *state) {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok || s == nil {
		return
	}
	session.Set(stateKey, *s)
}

func (p *Provider) provide(ctx context.Context, messages []*message.Message, opts ...agent.Option) ([]*message.Message, []agent.Option, error) {
	st := p.loadState(opts)
	// Persist the initial state so SetMode can read it.
	p.saveState(opts, st)

	tools := p.createTools(opts, st)
	outOpts := slices.Clone(opts)
	for _, t := range tools {
		outOpts = append(outOpts, agent.WithTool(t))
	}

	// Add instructions with mode info.
	instructionText := p.buildInstructions(st.CurrentMode)
	outOpts = append(outOpts, agent.WithInstructions(instructionText))

	outMessages := messages

	// If the mode was changed externally (e.g. via SetMode), inject a notification
	// so the agent clearly sees the change in conversation context.
	if st.PreviousMode != "" {
		outMessages = make([]*message.Message, 0, len(messages)+1)
		outMessages = append(outMessages, message.NewText(fmt.Sprintf(
			"[Mode changed: The operating mode has been switched from %q to %q. You must now adjust your behavior to match the %q mode.]",
			st.PreviousMode, st.CurrentMode, st.CurrentMode,
		)))
		outMessages = append(outMessages, messages...)
		st.PreviousMode = ""
		p.saveState(opts, st)
	}

	return outMessages, outOpts, nil
}

func (p *Provider) buildInstructions(currentMode string) string {
	var sb strings.Builder
	for _, m := range p.modes {
		fmt.Fprintf(&sb, "- \"%s\": %s\n", m.Name, m.Description)
	}
	modesText := sb.String()

	result := strings.ReplaceAll(p.instructions, "{available_modes}", modesText)
	result = strings.ReplaceAll(result, "{current_mode}", currentMode)
	return result
}

func (p *Provider) createTools(opts []agent.Option, st *state) []tool.FuncTool {
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
// If no state has been persisted yet, it returns the configured default mode.
func (p *Provider) GetMode(opts ...agent.Option) string {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return p.defaultMode
	}
	var s state
	if found, _ := session.Get(stateKey, &s); found {
		return s.CurrentMode
	}
	return p.defaultMode
}

// SetMode sets the operating mode in the session, validating it against
// the provider's configured modes. Returns an error if the mode is invalid
// or no session is available.
func (p *Provider) SetMode(mode string, opts ...agent.Option) error {
	if _, ok := p.validModes[mode]; !ok {
		return fmt.Errorf("agentmode: invalid mode %q", mode)
	}
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return fmt.Errorf("agentmode: no session available")
	}
	var s state
	if found, _ := session.Get(stateKey, &s); found {
		if s.CurrentMode != mode {
			s.PreviousMode = s.CurrentMode
			s.CurrentMode = mode
		}
	} else {
		s = state{CurrentMode: mode}
	}
	session.Set(stateKey, s)
	return nil
}
