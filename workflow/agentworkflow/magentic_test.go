// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"context"
	"encoding/json"
	"iter"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

// newMagenticScriptedOrchestrator returns an orchestrator agent that replies to
// task-ledger prompts with plain text and to progress-ledger prompts with the
// next scripted ledger (as JSON), in order.
func newMagenticScriptedOrchestrator(ledgers ...magenticProgressLedger) *agent.Agent {
	var next int
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			last := ""
			if len(messages) > 0 {
				last = messages[len(messages)-1].String()
			}
			var text string
			if strings.Contains(last, "JSON object") {
				idx := next
				if idx >= len(ledgers) {
					idx = len(ledgers) - 1
				}
				if idx < 0 {
					idx = 0
				}
				next++
				var ledger magenticProgressLedger
				if len(ledgers) > 0 {
					ledger = ledgers[idx]
				}
				encoded, _ := json.Marshal(ledger)
				text = string(encoded)
			} else {
				text = "PLAN: work the task"
			}
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: text}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "magentic-orchestrator", Run: run},
		agent.Config{ID: "orchestrator", Name: "orchestrator", DisableFuncAutoCall: true},
	)
}

func TestMagenticWorkflowBuilder_RequiresManager(t *testing.T) {
	_, err := NewMagenticWorkflowBuilder(newGroupChatLabelAgent("a", "a", "from-a")).Build()
	if err == nil || !strings.Contains(err.Error(), "manager") {
		t.Fatalf("Build error = %v, want a manager-required error", err)
	}
}

func TestMagenticWorkflowBuilder_RequiresAgents(t *testing.T) {
	_, err := NewMagenticWorkflowBuilder().WithManager(newMagenticScriptedOrchestrator()).Build()
	if err == nil {
		t.Fatal("Build should fail without participant agents")
	}
}

func TestMagenticWorkflowBuilder_RoutesToSelectedSpeakerThenTerminates(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "a", "from-a")
	agentB := newGroupChatLabelAgent("b", "b", "from-b")
	orchestrator := newMagenticScriptedOrchestrator(
		magenticProgressLedger{IsProgressBeingMade: true, NextSpeaker: "a", Instruction: "start"},
		magenticProgressLedger{IsRequestSatisfied: true},
	)

	wf, err := NewMagenticWorkflowBuilder(agentA, agentB).
		WithManager(orchestrator).
		WithMaximumRoundCount(5).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	events := runGroupChatWorkflowTurn(t, wf, "hello")
	if got := collectGroupChatUpdateTexts(events); !slices.Equal(got, []string{"from-a"}) {
		t.Fatalf("update texts = %v, want [from-a] (only the selected speaker runs, then the request is satisfied)", got)
	}
}

func TestMagenticWorkflowBuilder_StallsExhaustResetBudgetAndTerminate(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "a", "from-a")
	// Always report no progress: the manager should re-plan on each stall and,
	// once the reset budget is spent, stop without selecting a speaker.
	orchestrator := newMagenticScriptedOrchestrator(
		magenticProgressLedger{IsProgressBeingMade: false, NextSpeaker: "a"},
	)

	wf, err := NewMagenticWorkflowBuilder(agentA).
		WithManager(orchestrator).
		WithMaximumStallCount(1).
		WithMaximumResetCount(1).
		WithMaximumRoundCount(50).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// With maxStalls=1 the stall count trips a re-plan on every second round;
	// with maxResets=1 the second re-plan exhausts the budget and terminates.
	// The speaker runs on rounds 1..3 and round 4 gives up — so the workflow
	// stops well short of the round cap (50), proving reset-based termination.
	events := runGroupChatWorkflowTurn(t, wf, "hello")
	got := collectGroupChatUpdateTexts(events)
	if len(got) == 0 || len(got) >= 50 {
		t.Fatalf("update count = %d, want a small bounded count (reset-terminated, not round-capped)", len(got))
	}
	for _, text := range got {
		if text != "from-a" {
			t.Fatalf("unexpected update %q, want only from-a", text)
		}
	}
}

func TestMagenticWorkflowBuilder_UnknownSpeakerTerminatesGracefully(t *testing.T) {
	agentA := newGroupChatLabelAgent("a", "a", "from-a")
	orchestrator := newMagenticScriptedOrchestrator(
		magenticProgressLedger{IsProgressBeingMade: true, NextSpeaker: "does-not-exist"},
	)

	wf, err := NewMagenticWorkflowBuilder(agentA).WithManager(orchestrator).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Must not error out or route to a non-participant; it ends the run.
	events := runGroupChatWorkflowTurn(t, wf, "hello")
	if got := collectGroupChatUpdateTexts(events); len(got) != 0 {
		t.Fatalf("update texts = %v, want none for an unknown next speaker", got)
	}
}

func TestParseMagenticProgressLedger(t *testing.T) {
	cases := []struct {
		name string
		text string
		want magenticProgressLedger
		ok   bool
	}{
		{
			name: "plain json",
			text: `{"is_request_satisfied":true,"next_speaker":"a"}`,
			want: magenticProgressLedger{IsRequestSatisfied: true, NextSpeaker: "a"},
			ok:   true,
		},
		{
			name: "fenced with prose",
			text: "Sure!\n```json\n{\"is_progress_being_made\":true,\"next_speaker\":\"b\",\"instruction\":\"go\"}\n```",
			want: magenticProgressLedger{IsProgressBeingMade: true, NextSpeaker: "b", Instruction: "go"},
			ok:   true,
		},
		{
			name: "no json",
			text: "I could not decide.",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseMagenticProgressLedger(tc.text)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("ledger = %+v, want %+v", got, tc.want)
			}
		})
	}
}
