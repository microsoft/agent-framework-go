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
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	defaultMagenticMaximumRounds = 30
	defaultMagenticMaximumStalls = 3
	defaultMagenticMaximumResets = 2

	magenticManagerStateKey = "magentic_manager_state"
)

// MagenticWorkflowBuilder fluently builds a Magentic (manager-orchestrated)
// multi-agent workflow.
//
// A dedicated orchestrator agent maintains a task ledger (the known facts and a
// plan) and, each round, evaluates a progress ledger to decide whether the
// request is satisfied, whether progress has stalled, and which participant
// should speak next. When progress stalls it re-plans (rebuilding the task
// ledger); after exhausting its reset budget it stops. This implements the
// Magentic-One orchestration pattern and mirrors the .NET MagenticBuilder.
//
// It is a convenience wrapper around [NewGroupChatWorkflowBuilder]: the
// orchestrator drives [GroupChatManager.SelectNextAgent] and
// [GroupChatManager.ShouldTerminate].
type MagenticWorkflowBuilder struct {
	name         string
	description  string
	orchestrator *agent.Agent
	agents       []*agent.Agent
	maxRounds    int
	maxStalls    int
	maxResets    int
	outputs      outputDesignations
	err          error
}

// NewMagenticWorkflowBuilder creates a builder for a Magentic workflow over the
// given participant agents. Use [MagenticWorkflowBuilder.WithManager] to supply
// the orchestrator agent, which is required.
func NewMagenticWorkflowBuilder(agents ...*agent.Agent) *MagenticWorkflowBuilder {
	return &MagenticWorkflowBuilder{agents: slices.Clone(agents)}
}

// WithManager sets the orchestrator agent that plans and routes each round.
// It is required.
func (b *MagenticWorkflowBuilder) WithManager(orchestrator *agent.Agent) *MagenticWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.orchestrator = orchestrator
	return b
}

// WithMaximumRoundCount sets the maximum number of participant turns before the
// workflow stops. The zero value uses the default (30).
func (b *MagenticWorkflowBuilder) WithMaximumRoundCount(count int) *MagenticWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.maxRounds = count
	return b
}

// WithMaximumStallCount sets how many consecutive rounds without progress are
// tolerated before the orchestrator re-plans. The zero value uses the default (3).
func (b *MagenticWorkflowBuilder) WithMaximumStallCount(count int) *MagenticWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.maxStalls = count
	return b
}

// WithMaximumResetCount sets how many times the orchestrator may re-plan (reset
// the task ledger) before giving up. The zero value uses the default (2).
func (b *MagenticWorkflowBuilder) WithMaximumResetCount(count int) *MagenticWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.maxResets = count
	return b
}

// WithName sets the workflow name.
func (b *MagenticWorkflowBuilder) WithName(name string) *MagenticWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.name = name
	return b
}

// WithDescription sets the workflow description.
func (b *MagenticWorkflowBuilder) WithDescription(description string) *MagenticWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.description = description
	return b
}

// WithOutputFrom designates agents as terminal workflow output sources.
func (b *MagenticWorkflowBuilder) WithOutputFrom(agents ...*agent.Agent) *MagenticWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputs, b.err = b.outputs.withOutputFrom(agents...)
	return b
}

// WithIntermediateOutputFrom designates agents as intermediate workflow output sources.
func (b *MagenticWorkflowBuilder) WithIntermediateOutputFrom(agents ...*agent.Agent) *MagenticWorkflowBuilder {
	if b == nil || b.err != nil {
		return b
	}
	b.outputs, b.err = b.outputs.withIntermediateOutputFrom(agents...)
	return b
}

// Build builds the Magentic workflow.
func (b *MagenticWorkflowBuilder) Build() (*workflow.Workflow, error) {
	if b == nil {
		return nil, fmt.Errorf("agentworkflow: MagenticWorkflowBuilder is nil")
	}
	if b.err != nil {
		return nil, b.err
	}
	if err := validateBuilderAgents("MagenticWorkflowBuilder", b.agents); err != nil {
		return nil, err
	}
	if b.orchestrator == nil {
		return nil, fmt.Errorf("agentworkflow: MagenticWorkflowBuilder requires a manager agent set via WithManager")
	}

	opts := magenticOptions{maxRounds: b.maxRounds, maxStalls: b.maxStalls, maxResets: b.maxResets}
	factory := func(agents []*agent.Agent) *GroupChatManager {
		return newMagenticManager(b.orchestrator, agents, opts)
	}

	inner := NewGroupChatWorkflowBuilder(factory, b.agents...).
		WithName(b.name).
		WithDescription(b.description)
	inner.outputDesignations = b.outputs
	return inner.Build()
}

type magenticOptions struct {
	maxRounds int
	maxStalls int
	maxResets int
}

// magenticProgressLedger is the per-round JSON the orchestrator returns.
type magenticProgressLedger struct {
	IsRequestSatisfied  bool   `json:"is_request_satisfied"`
	IsProgressBeingMade bool   `json:"is_progress_being_made"`
	IsInLoop            bool   `json:"is_in_loop"`
	NextSpeaker         string `json:"next_speaker"`
	Instruction         string `json:"instruction"`
}

// magenticManagerState is the checkpoint-persisted manager state.
type magenticManagerState struct {
	TaskLedger string `json:"task_ledger"`
	RoundCount int    `json:"round_count"`
	StallCount int    `json:"stall_count"`
	ResetCount int    `json:"reset_count"`
	Done       bool   `json:"done"`
}

type magenticManager struct {
	manager      GroupChatManager
	orchestrator *agent.Agent
	agents       []*agent.Agent
	maxRounds    int
	maxStalls    int
	maxResets    int

	state magenticManagerState
}

func newMagenticManager(orchestrator *agent.Agent, agents []*agent.Agent, opts magenticOptions) *GroupChatManager {
	m := &magenticManager{
		orchestrator: orchestrator,
		agents:       slices.Clone(agents),
		maxRounds:    cmpOrDefault(opts.maxRounds, defaultMagenticMaximumRounds),
		maxStalls:    cmpOrDefault(opts.maxStalls, defaultMagenticMaximumStalls),
		maxResets:    cmpOrDefault(opts.maxResets, defaultMagenticMaximumResets),
	}
	m.manager = GroupChatManager{
		SelectNextAgent:      m.selectNextAgent,
		ShouldTerminate:      m.shouldTerminate,
		Reset:                m.reset,
		OnCheckpoint:         m.onCheckpoint,
		OnCheckpointRestored: m.onCheckpointRestored,
	}
	return &m.manager
}

func cmpOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func (m *magenticManager) selectNextAgent(ctx context.Context, history []*message.Message) (*agent.Agent, error) {
	if m == nil || m.state.Done || len(m.agents) == 0 {
		return nil, nil
	}

	// Build the task ledger (facts and a plan) once, on the first round.
	if m.state.TaskLedger == "" {
		ledger, err := m.buildTaskLedger(ctx, history)
		if err != nil {
			return nil, err
		}
		m.state.TaskLedger = ledger
	}

	progress, err := m.evaluateProgress(ctx, history)
	if err != nil {
		return nil, err
	}

	if progress.IsRequestSatisfied {
		m.state.Done = true
		return nil, nil
	}

	// Track stalls: a round that makes no progress or loops counts against the
	// stall budget; a productive round clears it.
	if !progress.IsProgressBeingMade || progress.IsInLoop {
		m.state.StallCount++
	} else {
		m.state.StallCount = 0
	}

	// Too many stalls trigger a re-plan; too many re-plans stop the workflow.
	if m.state.StallCount > m.maxStalls {
		m.state.ResetCount++
		if m.state.ResetCount > m.maxResets {
			m.state.Done = true
			return nil, nil
		}
		m.state.StallCount = 0
		ledger, err := m.buildTaskLedger(ctx, history)
		if err != nil {
			return nil, err
		}
		m.state.TaskLedger = ledger
	}

	next := m.resolveAgent(progress.NextSpeaker)
	if next == nil {
		// The orchestrator named an unknown participant; end gracefully rather
		// than route to a non-participant (which the host would reject).
		m.state.Done = true
		return nil, nil
	}
	m.state.RoundCount++
	return next, nil
}

func (m *magenticManager) shouldTerminate(_ context.Context, _ []*message.Message, iterationCount int) (bool, error) {
	if m == nil || m.state.Done {
		return true, nil
	}
	return iterationCount >= m.maxRounds || m.state.RoundCount >= m.maxRounds, nil
}

// resolveAgent finds the participant named by the orchestrator, matching on the
// agent name first and the ID second, case-insensitively.
func (m *magenticManager) resolveAgent(name string) *agent.Agent {
	target := strings.TrimSpace(strings.ToLower(name))
	if target == "" {
		return nil
	}
	for _, candidate := range m.agents {
		if strings.ToLower(candidate.Name()) == target || strings.ToLower(candidate.ID()) == target {
			return candidate
		}
	}
	return nil
}

func (m *magenticManager) buildTaskLedger(ctx context.Context, history []*message.Message) (string, error) {
	prompt := "You are the orchestrator of a team of AI agents: " + m.participantList() + ".\n" +
		"Review the conversation so far and produce a brief working plan for the team: " +
		"list the known facts, the facts still to be discovered, and a short numbered plan. " +
		"Respond with plain text only."
	return m.runOrchestrator(ctx, history, prompt)
}

func (m *magenticManager) evaluateProgress(ctx context.Context, history []*message.Message) (magenticProgressLedger, error) {
	prompt := "You are the orchestrator of a team of AI agents: " + m.participantList() + ".\n" +
		"Current plan:\n" + m.state.TaskLedger + "\n\n" +
		"Based on the conversation, decide how the team should proceed. " +
		"Respond with ONLY a JSON object of the form:\n" +
		`{"is_request_satisfied": <bool>, "is_progress_being_made": <bool>, "is_in_loop": <bool>, ` +
		`"next_speaker": "<one of: ` + m.participantList() + `>", "instruction": "<what that agent should do next>"}`
	text, err := m.runOrchestrator(ctx, history, prompt)
	if err != nil {
		return magenticProgressLedger{}, err
	}

	ledger, ok := parseMagenticProgressLedger(text)
	if !ok {
		// The orchestrator did not return parseable JSON. Treat this as an
		// unproductive round so the stall/reset machinery can recover rather
		// than failing the whole workflow.
		return magenticProgressLedger{IsProgressBeingMade: false}, nil
	}
	return ledger, nil
}

func (m *magenticManager) runOrchestrator(ctx context.Context, history []*message.Message, instruction string) (string, error) {
	messages := make([]*message.Message, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: instruction}},
	})
	resp, err := m.orchestrator.Run(ctx, messages).Collect()
	if err != nil {
		return "", fmt.Errorf("agentworkflow: magentic orchestrator failed: %w", err)
	}
	return resp.String(), nil
}

func (m *magenticManager) participantList() string {
	names := make([]string, len(m.agents))
	for i, a := range m.agents {
		names[i] = cmpOrString(a.Name(), a.ID())
	}
	return strings.Join(names, ", ")
}

func cmpOrString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// parseMagenticProgressLedger extracts the JSON object from the orchestrator's
// reply, tolerating surrounding prose or markdown code fences.
func parseMagenticProgressLedger(text string) (magenticProgressLedger, bool) {
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return magenticProgressLedger{}, false
	}
	var ledger magenticProgressLedger
	if err := json.Unmarshal([]byte(text[start:end+1]), &ledger); err != nil {
		return magenticProgressLedger{}, false
	}
	return ledger, true
}

func (m *magenticManager) reset() {
	if m == nil {
		return
	}
	m.state = magenticManagerState{}
}

func (m *magenticManager) onCheckpoint(ctx *workflow.Context) error {
	return ctx.QueueStateUpdate(magenticManagerStateKey, "", m.state)
}

func (m *magenticManager) onCheckpointRestored(ctx *workflow.Context) error {
	value, err := ctx.ReadState(magenticManagerStateKey, "")
	if err != nil {
		return err
	}
	state, err := magenticManagerStateFromAny(value)
	if err != nil {
		return err
	}
	m.state = magenticManagerState{}
	if state != nil {
		m.state = *state
	}
	return nil
}

func magenticManagerStateFromAny(value any) (*magenticManagerState, error) {
	if value == nil {
		return nil, nil
	}
	switch state := value.(type) {
	case magenticManagerState:
		return &state, nil
	case *magenticManagerState:
		return state, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var state magenticManagerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
