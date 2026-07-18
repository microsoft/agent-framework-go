// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	handoffToolNamePrefix  = "handoff_to_"
	handoffManagerStateKey = "handoff_state"
)

// HandoffWorkflowBuilder fluently builds handoff workflows.
//
// A handoff workflow is a group chat whose next speaker is chosen by the current
// agent itself: each agent is given a handoff tool for every target it may hand
// off to, and calling that tool routes the shared conversation to the target.
// The first agent passed to [NewHandoffWorkflowBuilder] is the entry agent that
// receives the initial input. A turn in which the speaking agent calls no
// handoff tool ends the workflow and yields the accumulated conversation.
//
// Participant agents must have automatic function-tool calling enabled (the
// default; that is, [agent.Config.DisableFuncAutoCall] must be false) so the
// injected handoff tools execute and the resulting tool call is recorded in the
// conversation for routing. An agent that surfaces a handoff call without
// executing it would instead raise it as an unresolved function call and stall
// the run.
type HandoffWorkflowBuilder struct {
	name                  string
	description           string
	agents                []*agent.Agent
	entry                 *agent.Agent
	handoffs              map[*agent.Agent][]*agent.Agent
	handoffOrder          []*agent.Agent
	outputDesignations    outputDesignations
	maximumIterationCount int
	err                   error
}

// NewHandoffWorkflowBuilder creates a builder for a handoff workflow. The first
// agent is the entry agent that receives the initial input.
func NewHandoffWorkflowBuilder(agents ...*agent.Agent) *HandoffWorkflowBuilder {
	b := &HandoffWorkflowBuilder{
		agents:   slices.Clone(agents),
		handoffs: make(map[*agent.Agent][]*agent.Agent),
	}
	if len(agents) > 0 {
		b.entry = agents[0]
	}
	return b
}

// WithName sets the workflow name.
func (b *HandoffWorkflowBuilder) WithName(name string) *HandoffWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.name = name
	return b
}

// WithDescription sets the workflow description.
func (b *HandoffWorkflowBuilder) WithDescription(description string) *HandoffWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.description = description
	return b
}

// WithHandoff allows from to hand off the conversation to each of targets. It
// may be called multiple times to extend an agent's set of targets. Both from
// and every target must be participants passed to [NewHandoffWorkflowBuilder].
func (b *HandoffWorkflowBuilder) WithHandoff(from *agent.Agent, targets ...*agent.Agent) *HandoffWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	if from == nil {
		b.err = fmt.Errorf("agentworkflow: HandoffWorkflowBuilder handoff source is nil")
		return b
	}
	for index, target := range targets {
		if target == nil {
			b.err = fmt.Errorf("agentworkflow: HandoffWorkflowBuilder handoff target at index %d is nil", index)
			return b
		}
	}
	if _, ok := b.handoffs[from]; !ok {
		b.handoffOrder = append(b.handoffOrder, from)
	}
	b.handoffs[from] = append(b.handoffs[from], targets...)
	return b
}

// WithMaximumIterationCount caps the number of agent turns before the workflow
// ends, guarding against handoff loops (for example two agents that keep handing
// off to each other). A non-positive value uses the default of 40.
func (b *HandoffWorkflowBuilder) WithMaximumIterationCount(count int) *HandoffWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.maximumIterationCount = count
	return b
}

// WithOutputFrom designates agents as terminal workflow output sources.
func (b *HandoffWorkflowBuilder) WithOutputFrom(agents ...*agent.Agent) *HandoffWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputDesignations, b.err = b.outputDesignations.withOutputFrom(agents...)
	return b
}

// WithIntermediateOutputFrom designates agents as intermediate workflow output sources.
func (b *HandoffWorkflowBuilder) WithIntermediateOutputFrom(agents ...*agent.Agent) *HandoffWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputDesignations, b.err = b.outputDesignations.withIntermediateOutputFrom(agents...)
	return b
}

// Build builds the handoff workflow.
func (b *HandoffWorkflowBuilder) Build() (*workflow.Workflow, error) {
	if b == nil {
		return nil, fmt.Errorf("agentworkflow: HandoffWorkflowBuilder is nil")
	}
	if b.err != nil {
		return nil, b.err
	}
	if err := validateBuilderAgents("HandoffWorkflowBuilder", b.agents); err != nil {
		return nil, err
	}

	members := make(map[*agent.Agent]struct{}, len(b.agents))
	for _, participant := range b.agents {
		members[participant] = struct{}{}
	}

	// Build, per source agent, the handoff tools it is offered, and a global
	// map from handoff tool name to target agent used by the manager to route.
	toolTargets := make(map[string]*agent.Agent)
	handoffToolsByAgent := make(map[*agent.Agent][]tool.Tool)
	for _, from := range b.handoffOrder {
		if _, ok := members[from]; !ok {
			return nil, fmt.Errorf("agentworkflow: handoff source %q is not a participant in this handoff workflow", agentNameForError(from))
		}
		seen := make(map[*agent.Agent]struct{})
		for _, target := range b.handoffs[from] {
			if target == from {
				return nil, fmt.Errorf("agentworkflow: agent %q cannot hand off to itself", agentNameForError(from))
			}
			if _, ok := members[target]; !ok {
				return nil, fmt.Errorf("agentworkflow: handoff target %q is not a participant in this handoff workflow", agentNameForError(target))
			}
			if _, dup := seen[target]; dup {
				continue
			}
			seen[target] = struct{}{}
			toolName := handoffToolName(target)
			if existing, ok := toolTargets[toolName]; ok && existing != target {
				return nil, fmt.Errorf("agentworkflow: handoff targets %q and %q map to the same handoff tool name %q; give the agents distinct names", agentNameForError(existing), agentNameForError(target), toolName)
			}
			toolTargets[toolName] = target
			handoffToolsByAgent[from] = append(handoffToolsByAgent[from], newHandoffTool(target, toolName))
		}
	}

	participants := b.agents
	participantBindings := make([]workflow.ExecutorBinding, len(participants))
	bindingsByAgent := make(map[*agent.Agent]workflow.ExecutorBinding, len(participants))
	bindingsByAgentID := make(map[string]workflow.ExecutorBinding, len(participants))
	for index, currentAgent := range participants {
		cfg := Config{DisableForwardIncomingMessages: true}
		for _, handoffTool := range handoffToolsByAgent[currentAgent] {
			cfg.RunOptions = append(cfg.RunOptions, agent.WithTool(handoffTool))
		}
		binding := New(currentAgent, cfg)
		participantBindings[index] = binding
		bindingsByAgent[currentAgent] = binding
		bindingsByAgentID[currentAgent.ID()] = binding
	}

	entry := b.entry
	maximumIterationCount := b.maximumIterationCount
	// The participant set is fixed at build time, so the factory closes over the
	// entry agent and handoff routing table and ignores its agents argument.
	managerFactory := func([]*agent.Agent) *GroupChatManager {
		return newHandoffManager(entry, toolTargets, maximumIterationCount)
	}

	host := newGroupChatHostBinding(participants, participantBindings, bindingsByAgent, bindingsByAgentID, managerFactory)
	builder := applyBuilderMetadata(workflow.NewBuilder(host), b.name, b.description)
	for _, participant := range participantBindings {
		builder = builder.AddEdge(host, participant).AddEdge(participant, host)
	}
	var err error
	builder, err = applyOutputDesignations(builder, b.outputDesignations, bindingsByAgent, "handoff", func() {
		builder = builder.WithOutputFrom(host).WithIntermediateOutputFrom(participantBindings...)
	})
	if err != nil {
		return nil, err
	}
	return builder.Build()
}

// handoffToolName derives the handoff tool name presented to the model for a
// target agent, from its name (or ID when unnamed).
func handoffToolName(target *agent.Agent) string {
	base := target.Name()
	if strings.TrimSpace(base) == "" {
		base = target.ID()
	}
	return handoffToolNamePrefix + sanitizeToolNameSegment(base)
}

// sanitizeToolNameSegment maps an arbitrary agent identifier to the character
// set accepted for function tool names ([a-zA-Z0-9_-]).
func sanitizeToolNameSegment(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	if sb.Len() == 0 {
		return "agent"
	}
	return sb.String()
}

// handoffToolArgs is the (empty) argument object for a handoff tool. The model
// calls the tool with {} to hand off; no parameters are required.
type handoffToolArgs struct{}

func newHandoffTool(target *agent.Agent, name string) tool.Tool {
	targetName := agentNameForError(target)
	return functool.MustNew(
		functool.Config{
			Name:        name,
			Description: fmt.Sprintf("Hand off the conversation to the %q agent. Call this when the request should be handled by that agent instead of you.", targetName),
		},
		func(context.Context, handoffToolArgs) (string, error) {
			return fmt.Sprintf("Handing off to %s.", targetName), nil
		},
	)
}

// handoffManager is a [GroupChatManager] whose SelectNextAgent routes to the
// target of the most recent handoff tool call produced by the previous speaker.
type handoffManager struct {
	manager               GroupChatManager
	entry                 *agent.Agent
	toolTargets           map[string]*agent.Agent
	maximumIterationCount int
	started               bool
	cursor                int
}

func newHandoffManager(entry *agent.Agent, toolTargets map[string]*agent.Agent, maximumIterationCount int) *GroupChatManager {
	m := &handoffManager{entry: entry, toolTargets: toolTargets, maximumIterationCount: maximumIterationCount}
	m.manager = GroupChatManager{
		SelectNextAgent:      m.selectNextAgent,
		ShouldTerminate:      m.shouldTerminate,
		Reset:                m.reset,
		OnCheckpoint:         m.onCheckpoint,
		OnCheckpointRestored: m.onCheckpointRestored,
	}
	return &m.manager
}

func (m *handoffManager) shouldTerminate(_ context.Context, _ []*message.Message, iterationCount int) (bool, error) {
	limit := m.maximumIterationCount
	if limit <= 0 {
		limit = defaultGroupChatMaximumIterations
	}
	return iterationCount >= limit, nil
}

func (m *handoffManager) selectNextAgent(_ context.Context, history []*message.Message) (*agent.Agent, error) {
	if m == nil {
		return nil, nil
	}
	if !m.started {
		m.started = true
		m.cursor = len(history)
		return m.entry, nil
	}
	start := m.cursor
	if start < 0 {
		start = 0
	}
	if start > len(history) {
		// A cursor beyond the history (only reachable from an inconsistent
		// restore) means there are no new messages to inspect; end cleanly
		// rather than rescanning the whole history and routing on a stale call.
		start = len(history)
	}
	m.cursor = len(history)
	// The most recent handoff call in the previous speaker's turn wins; a turn
	// with no handoff call returns nil, ending the workflow.
	var target *agent.Agent
	for _, msg := range history[start:] {
		if msg == nil {
			continue
		}
		for _, content := range msg.Contents {
			call, ok := content.(*message.FunctionCallContent)
			if !ok {
				continue
			}
			if next, ok := m.toolTargets[call.Name]; ok {
				target = next
			}
		}
	}
	return target, nil
}

func (m *handoffManager) reset() {
	m.started = false
	m.cursor = 0
}

type handoffManagerState struct {
	Started bool
	Cursor  int
}

func (m *handoffManager) onCheckpoint(ctx *workflow.Context) error {
	return ctx.QueueStateUpdate(handoffManagerStateKey, "", handoffManagerState{Started: m.started, Cursor: m.cursor})
}

func (m *handoffManager) onCheckpointRestored(ctx *workflow.Context) error {
	value, err := ctx.ReadState(handoffManagerStateKey, "")
	if err != nil {
		return err
	}
	state, err := handoffManagerStateFromAny(value)
	if err != nil {
		return err
	}
	m.started = false
	m.cursor = 0
	if state != nil {
		m.started = state.Started
		m.cursor = state.Cursor
	}
	return nil
}

func handoffManagerStateFromAny(value any) (*handoffManagerState, error) {
	if value == nil {
		return nil, nil
	}
	switch state := value.(type) {
	case handoffManagerState:
		return &state, nil
	case *handoffManagerState:
		return state, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var state handoffManagerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
