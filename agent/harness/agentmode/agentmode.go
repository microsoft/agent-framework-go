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
	"runtime"
	"strings"
	"sync"
	"weak"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

const stateKey = "agentModeState"

const defaultInstructions = `## Agent Mode

- You can operate in different modes. Depending on the mode you are in, you will be required to follow different processes.
- You must check the current mode after any user input, since the user may have changed the mode themselves,
  e.g. the user may have switched to 'plan' mode after a previous research task finished in 'execute' mode, meaning they want to review a plan first before execution.

Use the mode_get tool to check your current operating mode.
Use the mode_set tool to switch between modes as your work progresses. Only use mode_set if the user explicitly instructs/allows you to change modes.

You are currently operating in the {current_mode} mode.

### Mandatory Mode based Workflow

For every new substantive user request, including short factual questions, your behavior is determined by the mode you are in.

{available_modes}`

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
		Name: "plan",
		Description: `Use this mode when analyzing requirements, breaking down tasks, and creating plans. This is the interactive mode — ask clarifying questions, discuss options, and get user approval before proceeding.

Process to follow when in plan mode:
1. Analyze the request with the purpose of building a research plan.
2. Create a list of todo items.
3. If needed, use the provided tools to do some exploratory checks to help build a plan and determine what clarifying questions you may need from the user.
4. Ask for clarifications from the user where needed.
  1. Ask each clarification one by one.
  2. When asking for clarification and you have specific options in mind, present them to the user, so they can choose the option instead of having to retype the entire response.
  3. Do not proceed until you have received all the needed clarifications.
  4. Do short exploratory research if it helps with being able to ask sensible clarifications from the user.
5. Write the plan to a memory file, so that it is retained even if compaction happens. Make sure to update the plan file if the user requests changes.
6. Present the plan to the user and ask for approval to switch to execute mode and process the plan.
7. When approval is granted, always switch to execute mode (using the ` + "`mode_set`" + ` tool), and follow the steps for *Execute mode*.`,
	},
	{
		Name: "execute",
		Description: `Use this mode when carrying out approved plans. Work autonomously using your best judgment — do not ask the user questions or wait for feedback.

Process to follow when in execute mode:
1. If you don't have a plan or tasks yet, analyze the user request and create tasks and a plan. (**Skip this step if you came from plan mode**)
2. Work autonomously — use your best judgment to make decisions and keep progressing without asking the user questions. The goal is to have a complete, useful result ready when the user returns.
3. If you encounter ambiguity or an unexpected situation during execution, choose the most reasonable option, note your choice, and keep going.
4. Mark tasks as completed as you finish them.
5. Continue working, thinking and calling tools until you have the research result for the user.`,
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

	p.provider = agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID: "AgentModeProvider",
		Provide:  p.provide,
	})
	return p
}

// Provider is an agent mode context provider.
// Use [New] to create. Provider can be used directly in agent configuration.
type Provider struct {
	provider     agent.ContextProvider
	modes        []Mode
	defaultMode  string
	instructions string
	validModes   map[string]struct{}

	sessionLocks    sync.Map // map[weak.Pointer[agent.Session]]*sync.Mutex
	nullSessionLock sync.Mutex
}

// getSessionLock returns a per-session mutex that guards the session state
// against concurrent tool invocations, which toolautocall runs on separate
// goroutines when AllowConcurrentInvocations is enabled.
//
// The registry is keyed by session object identity via a weak pointer, not by
// Session.ServiceID(): a service ID may be empty (causing unrelated sessions to
// collide), shared across distinct sessions, or mutated during a session's
// lifetime — any of which would break the guarantee that a given session always
// maps to the same lock. When no session is available, a shared fallback lock is
// returned so state access is still serialized.
//
// Weak keys do not keep sessions alive and do not remove map entries on their
// own, so a runtime cleanup deletes the entry once the session is collected,
// keeping the registry from growing unbounded.
func (p *Provider) getSessionLock(opts []agent.Option) *sync.Mutex {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok || session == nil {
		return &p.nullSessionLock
	}
	key := weak.Make(session)
	if existing, ok := p.sessionLocks.Load(key); ok {
		return existing.(*sync.Mutex)
	}
	actual, loaded := p.sessionLocks.LoadOrStore(key, &sync.Mutex{})
	if !loaded {
		// First registration for this session: arrange to drop the entry when
		// the session is garbage collected. The cleanup must not capture the
		// session itself (only the weak key), or it would keep it alive.
		runtime.AddCleanup(session, func(k weak.Pointer[agent.Session]) {
			p.sessionLocks.Delete(k)
		}, key)
	}
	return actual.(*sync.Mutex)
}

func (p *Provider) Invoking(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	return p.provider.Invoking(ctx, invoking)
}

func (p *Provider) Invoked(ctx context.Context, invoked agent.InvokedContext) error {
	return p.provider.Invoked(ctx, invoked)
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

func (p *Provider) provide(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
	opts := invoking.Options

	var outMessages []*message.Message

	mu := p.getSessionLock(opts)
	mu.Lock()
	st := p.loadState(opts)
	// Persist the initial state so SetMode can read it.
	p.saveState(opts, st)

	// If the mode was changed externally (e.g. via SetMode), inject a notification
	// so the agent clearly sees the change in conversation context.
	if st.PreviousMode != "" {
		outMessages = append(outMessages, message.NewText(fmt.Sprintf(
			"[Mode changed: The operating mode has been switched from %q to %q. You must now adjust your behavior to match the %q mode.]",
			st.PreviousMode, st.CurrentMode, st.CurrentMode,
		)))
		st.PreviousMode = ""
		p.saveState(opts, st)
	}
	currentMode := st.CurrentMode
	mu.Unlock()

	tools := p.createTools(opts)
	var outOpts []agent.Option
	for _, t := range tools {
		outOpts = append(outOpts, agent.WithTool(t))
	}

	// Add instructions with mode info.
	instructionText := p.buildInstructions(currentMode)
	outOpts = append(outOpts, agent.WithInstructions(instructionText))

	return outMessages, outOpts, nil
}

func (p *Provider) buildInstructions(currentMode string) string {
	var sb strings.Builder
	for _, m := range p.modes {
		fmt.Fprintf(&sb, "#### %s\n\n%s\n\n", m.Name, strings.TrimRight(m.Description, "\n"))
	}
	modesText := strings.TrimRight(sb.String(), "\n")

	result := strings.ReplaceAll(p.instructions, "{available_modes}", modesText)
	result = strings.ReplaceAll(result, "{current_mode}", currentMode)
	return result
}

func (p *Provider) createTools(opts []agent.Option) []tool.FuncTool {
	modeNames := make([]string, len(p.modes))
	for i, m := range p.modes {
		modeNames[i] = m.Name
	}
	modeNamesDisplay := strings.Join(modeNames, "\", \"")

	setTool := functool.MustNew(
		functool.Config{
			Name:        "mode_set",
			Description: fmt.Sprintf("Switch the agent's operating mode. Supported modes: \"%s\".", modeNamesDisplay),
		},
		func(ctx context.Context, mode string) (string, error) {
			if _, ok := p.validModes[mode]; !ok {
				return "", fmt.Errorf("invalid mode: %q. Supported modes: \"%s\"", mode, modeNamesDisplay)
			}
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			st.CurrentMode = mode
			p.saveState(opts, st)
			return fmt.Sprintf("Mode changed to %q.", mode), nil
		},
	)

	getTool := functool.MustNew(
		functool.Config{
			Name:        "mode_get",
			Description: "Get the agent's current operating mode.",
		},
		func(ctx context.Context, _ struct{}) (string, error) {
			mu := p.getSessionLock(opts)
			mu.Lock()
			defer mu.Unlock()
			st := p.loadState(opts)
			return st.CurrentMode, nil
		},
	)

	return []tool.FuncTool{setTool, getTool}
}

// GetMode returns the current operating mode from the session.
// If no state has been persisted yet, it returns the configured default mode.
func (p *Provider) GetMode(opts ...agent.Option) string {
	mu := p.getSessionLock(opts)
	mu.Lock()
	defer mu.Unlock()
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
	mu := p.getSessionLock(opts)
	mu.Lock()
	defer mu.Unlock()
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
