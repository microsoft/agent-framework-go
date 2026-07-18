// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"context"
	"iter"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

// newScriptedHandoffAgent returns a stub agent that emits, on each successive
// run, the next content set from script (repeating the final set once the
// script is exhausted). DisableFuncAutoCall keeps the emitted content verbatim,
// so a scripted handoff FunctionCallContent reaches the manager unchanged.
func newScriptedHandoffAgent(id string, name string, script [][]message.Content) *agent.Agent {
	index := 0
	run := func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			var contents []message.Content
			if len(script) > 0 {
				if index < len(script) {
					contents = script[index]
				} else {
					contents = script[len(script)-1]
				}
			}
			index++
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: name,
				Contents:   contents,
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "handoff-script", Run: run},
		agent.Config{ID: id, Name: name, DisableFuncAutoCall: true},
	)
}

func handoffTextContent(s string) message.Content {
	return &message.TextContent{Text: s}
}

// handoffTurn returns the content an agent emits to hand off to toolName: the
// handoff tool call together with its result, mirroring what an auto-executed
// handoff tool produces (a resolved call, not an unterminated one).
func handoffTurn(text string, toolName string) []message.Content {
	var contents []message.Content
	if text != "" {
		contents = append(contents, handoffTextContent(text))
	}
	callID := toolName + "_call"
	return append(
		contents,
		&message.FunctionCallContent{CallID: callID, Name: toolName},
		&message.FunctionResultContent{CallID: callID, Result: "ok"},
	)
}

func answerTurn(text string) []message.Content {
	return []message.Content{handoffTextContent(text)}
}

func TestHandoffWorkflow_RoutesOnHandoffToolCall(t *testing.T) {
	triage := newScriptedHandoffAgent("triage", "triage", [][]message.Content{
		handoffTurn("routing you to billing", "handoff_to_billing"),
	})
	billing := newScriptedHandoffAgent("billing", "billing", [][]message.Content{
		answerTurn("billing handled it"),
	})

	wf, err := NewHandoffWorkflowBuilder(triage, billing).
		WithHandoff(triage, billing).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	texts := collectGroupChatOutputTexts(runGroupChatWorkflowTurn(t, wf, "I have a billing question"))
	if !slices.Contains(texts, "billing handled it") {
		t.Fatalf("expected billing to handle the conversation, got outputs: %v", texts)
	}
}

func TestHandoffWorkflow_Handback(t *testing.T) {
	// triage hands off to billing, billing hands back to triage, triage answers.
	triage := newScriptedHandoffAgent("triage", "triage", [][]message.Content{
		handoffTurn("", "handoff_to_billing"),
		answerTurn("triage final answer"),
	})
	billing := newScriptedHandoffAgent("billing", "billing", [][]message.Content{
		handoffTurn("", "handoff_to_triage"),
	})

	wf, err := NewHandoffWorkflowBuilder(triage, billing).
		WithHandoff(triage, billing).
		WithHandoff(billing, triage).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	texts := collectGroupChatOutputTexts(runGroupChatWorkflowTurn(t, wf, "start"))
	if !slices.Contains(texts, "triage final answer") {
		t.Fatalf("expected triage to produce the final answer after handback, got outputs: %v", texts)
	}
}

func TestHandoffWorkflow_EntryAnswersWithoutHandoff(t *testing.T) {
	solo := newScriptedHandoffAgent("solo", "solo", [][]message.Content{
		answerTurn("answered directly"),
	})

	wf, err := NewHandoffWorkflowBuilder(solo).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	texts := collectGroupChatOutputTexts(runGroupChatWorkflowTurn(t, wf, "hi"))
	if !slices.Contains(texts, "answered directly") {
		t.Fatalf("expected the entry agent to answer directly, got outputs: %v", texts)
	}
}

func TestHandoffWorkflow_BuildValidation(t *testing.T) {
	a := newScriptedHandoffAgent("a", "a", nil)
	b := newScriptedHandoffAgent("b", "b", nil)
	stranger := newScriptedHandoffAgent("x", "x", nil)

	tests := []struct {
		name    string
		build   func() (*workflow.Workflow, error)
		wantErr bool
	}{
		{name: "no agents", build: func() (*workflow.Workflow, error) { return NewHandoffWorkflowBuilder().Build() }, wantErr: true},
		{name: "nil agent", build: func() (*workflow.Workflow, error) { return NewHandoffWorkflowBuilder(a, nil).Build() }, wantErr: true},
		{name: "nil handoff source", build: func() (*workflow.Workflow, error) {
			return NewHandoffWorkflowBuilder(a, b).WithHandoff(nil, b).Build()
		}, wantErr: true},
		{name: "nil handoff target", build: func() (*workflow.Workflow, error) {
			return NewHandoffWorkflowBuilder(a, b).WithHandoff(a, nil).Build()
		}, wantErr: true},
		{name: "non-participant source", build: func() (*workflow.Workflow, error) {
			return NewHandoffWorkflowBuilder(a, b).WithHandoff(stranger, b).Build()
		}, wantErr: true},
		{name: "non-participant target", build: func() (*workflow.Workflow, error) {
			return NewHandoffWorkflowBuilder(a, b).WithHandoff(a, stranger).Build()
		}, wantErr: true},
		{name: "valid", build: func() (*workflow.Workflow, error) {
			return NewHandoffWorkflowBuilder(a, b).WithHandoff(a, b).Build()
		}, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, err := tt.build()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil (wf=%v)", wf)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if wf == nil {
				t.Fatal("expected a workflow, got nil")
			}
		})
	}
}

func TestHandoffManager_CheckpointRestoresState(t *testing.T) {
	triage := newScriptedHandoffAgent("triage", "triage", nil)
	billing := newScriptedHandoffAgent("billing", "billing", nil)
	toolTargets := map[string]*agent.Agent{"handoff_to_billing": billing}
	manager := newHandoffManager(triage, toolTargets)

	// First selection returns the entry agent and marks the manager started.
	userTurn := []*message.Message{{Role: message.RoleUser, Contents: []message.Content{handoffTextContent("hi")}}}
	if got, err := manager.SelectNextAgent(t.Context(), userTurn); err != nil || got != triage {
		t.Fatalf("first SelectNextAgent = (%v, %v), want (triage, nil)", got, err)
	}

	state := make(map[string]any)
	if err := checkpointGroupChatManager(newGroupChatStateContext(t.Context(), state), manager, 2); err != nil {
		t.Fatalf("checkpointGroupChatManager: %v", err)
	}
	if _, ok := state[groupChatManagerSubclassStateKeyPref+handoffManagerStateKey]; !ok {
		t.Fatalf("missing prefixed handoff manager state key")
	}

	// A fresh manager restored from the checkpoint must resume in the started
	// state: the next turn's handoff call routes to the target rather than
	// re-selecting the entry agent.
	restored := newHandoffManager(triage, toolTargets)
	if _, err := restoreGroupChatManagerCheckpoint(newGroupChatStateContext(t.Context(), state), restored); err != nil {
		t.Fatalf("restoreGroupChatManagerCheckpoint: %v", err)
	}
	history := append(slices.Clone(userTurn), &message.Message{Role: message.RoleAssistant, Contents: handoffTurn("", "handoff_to_billing")})
	next, err := restored.SelectNextAgent(t.Context(), history)
	if err != nil {
		t.Fatalf("SelectNextAgent restored: %v", err)
	}
	if next != billing {
		t.Fatalf("restored manager routed to %v, want billing (checkpoint did not restore started state)", next)
	}
}
