// Copyright (c) Microsoft. All rights reserved.

package workflowhosting_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

// TestConcurrentWorkflowBuilder_ReturnsErrorForNoAgents checks that the
// concurrent builder rejects an empty agent list.
func TestConcurrentWorkflowBuilder_ReturnsErrorForNoAgents(t *testing.T) {
	_, err := workflowhosting.NewConcurrentWorkflowBuilder().Build()
	if err == nil {
		t.Fatal("expected error for zero agents, got nil")
	}
}

func TestConcurrentWorkflowBuilder_ReturnsErrorForNilAgent(t *testing.T) {
	validAgent := newLabeledEchoAgent("a", "A", "from-a")
	for _, tt := range []struct {
		name   string
		agents []*agent.Agent
	}{
		{name: "only_nil", agents: []*agent.Agent{nil}},
		{name: "second_nil", agents: []*agent.Agent{validAgent, nil}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := workflowhosting.NewConcurrentWorkflowBuilder(tt.agents...).Build()
			if err == nil {
				t.Fatal("expected error for nil agent, got nil")
			}
			if !strings.Contains(err.Error(), "nil") {
				t.Fatalf("error = %q, want it to mention nil", err.Error())
			}
		})
	}
}

// TestConcurrentWorkflowBuilder_SingleAgent verifies that a single-agent concurrent
// workflow builds and emits the agent's output.
func TestConcurrentWorkflowBuilder_SingleAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	wf, err := workflowhosting.NewConcurrentWorkflowBuilder(a).WithName("single-concurrent").Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if wf.Name() != "single-concurrent" {
		t.Fatalf("workflow name = %q, want %q", wf.Name(), "single-concurrent")
	}

	events := runBuiltWorkflow(t, wf)
	texts := collectOutputTexts(events)
	if len(texts) != 1 || texts[0] != "from-a" {
		t.Errorf("got texts %v, want [from-a]", texts)
	}
	resultTexts := collectMessageTexts(collectOutputMessages(events))
	if !slices.Equal(resultTexts, []string{"from-a"}) {
		t.Errorf("result texts = %v, want [from-a]", resultTexts)
	}
}

// TestConcurrentWorkflowBuilder_MultiAgent verifies that a multi-agent concurrent
// workflow builds and emits output from all agents.
func TestConcurrentWorkflowBuilder_MultiAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	wf, err := workflowhosting.NewConcurrentWorkflowBuilder(a, b).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	events := runBuiltWorkflow(t, wf)
	texts := collectOutputTexts(events)

	want := map[string]bool{"from-a": true, "from-b": true}
	got := map[string]bool{}
	for _, text := range texts {
		got[text] = true
	}
	for label := range want {
		if !got[label] {
			t.Errorf("missing output %q; got all texts: %v", label, texts)
		}
	}

	resultTexts := collectMessageTexts(collectOutputMessages(events))
	if len(resultTexts) != 2 {
		t.Fatalf("result texts = %v, want two messages", resultTexts)
	}
	for label := range want {
		if !slices.Contains(resultTexts, label) {
			t.Errorf("missing result %q; got result texts: %v", label, resultTexts)
		}
	}
}

func TestConcurrentWorkflowBuilder_ExplicitOutputDesignationSuppressesDefaults(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	wf, err := workflowhosting.NewConcurrentWorkflowBuilder(a, b).
		WithOutputFrom(a).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	wantOutputID := workflowhosting.New(a, workflowhosting.Config{}).ID
	outputIDs := wf.OutputExecutorIDs()
	slices.Sort(outputIDs)
	if !slices.Equal(outputIDs, []string{wantOutputID}) {
		t.Fatalf("output executor IDs = %v, want [%s]", outputIDs, wantOutputID)
	}
	events := runBuiltWorkflow(t, wf)
	texts := collectOutputTexts(events)
	if !slices.Equal(texts, []string{"from-a"}) {
		t.Fatalf("output texts = %v, want [from-a]", texts)
	}
	if messages := collectOutputMessages(events); len(messages) != 0 {
		t.Fatalf("terminal aggregator output should be suppressed, got %v", collectMessageTexts(messages))
	}
}

func TestConcurrentWorkflowBuilder_ExplicitOutputDesignationRejectsNonParticipant(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	nonParticipant := newLabeledEchoAgent("outside", "Outside", "from-outside")

	for _, testCase := range []struct {
		name      string
		configure func(*workflowhosting.ConcurrentWorkflowBuilder) *workflowhosting.ConcurrentWorkflowBuilder
	}{
		{
			name: "terminal",
			configure: func(builder *workflowhosting.ConcurrentWorkflowBuilder) *workflowhosting.ConcurrentWorkflowBuilder {
				return builder.WithOutputFrom(nonParticipant)
			},
		},
		{
			name: "intermediate",
			configure: func(builder *workflowhosting.ConcurrentWorkflowBuilder) *workflowhosting.ConcurrentWorkflowBuilder {
				return builder.WithIntermediateOutputFrom(nonParticipant)
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := testCase.configure(workflowhosting.NewConcurrentWorkflowBuilder(a)).Build()
			if err == nil {
				t.Fatal("expected error for non-participant output designation, got nil")
			}
			if !strings.Contains(err.Error(), "not a participant") {
				t.Fatalf("error = %q, want it to mention not a participant", err.Error())
			}
		})
	}
}

func TestConcurrentWorkflowBuilder_UsesCustomAggregator(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	contextWasPresent := false
	wf, err := workflowhosting.NewConcurrentWorkflowBuilder(a, b).WithAggregator(func(ctx context.Context, lists [][]*message.Message) []*message.Message {
		contextWasPresent = ctx != nil
		return []*message.Message{{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.TextContent{Text: fmt.Sprintf("batches:%d", len(lists))},
			},
		}}
	}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	resultTexts := collectMessageTexts(collectOutputMessages(runBuiltWorkflow(t, wf)))
	if !slices.Equal(resultTexts, []string{"batches:2"}) {
		t.Fatalf("result texts = %v, want [batches:2]", resultTexts)
	}
	if !contextWasPresent {
		t.Fatal("expected aggregator to receive a context")
	}
}

func TestConcurrentWorkflowBuilder_ResetsEndExecutorBetweenTurns(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	callCount := 0
	wf, err := workflowhosting.NewConcurrentWorkflowBuilder(a, b).WithAggregator(func(_ context.Context, lists [][]*message.Message) []*message.Message {
		callCount++
		return []*message.Message{{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.TextContent{Text: fmt.Sprintf("turn:%d batches:%d", callCount, len(lists))},
			},
		}}
	}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	defer func() {
		if err := stream.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	first := collectMessageTexts(collectOutputMessages(runStreamingWorkflowTurn(t, ctx, stream, "first")))
	if !slices.Equal(first, []string{"turn:1 batches:2"}) {
		t.Fatalf("first result texts = %v, want [turn:1 batches:2]", first)
	}
	second := collectMessageTexts(collectOutputMessages(runStreamingWorkflowTurn(t, ctx, stream, "second")))
	if !slices.Equal(second, []string{"turn:2 batches:2"}) {
		t.Fatalf("second result texts = %v, want [turn:2 batches:2]", second)
	}
}

func TestConcurrentWorkflowBuilder_AgentsRunInParallel(t *testing.T) {
	barrier := &runBarrier{}
	wf, err := workflowhosting.NewConcurrentWorkflowBuilder(
		newBarrierDoubleEchoAgent("agent1", barrier),
		newBarrierDoubleEchoAgent("agent2", barrier),
	).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for range 3 {
		barrier.reset(2)
		events := runBuiltWorkflowWithText(t, wf, "abc")
		updateText := strings.Join(collectOutputTexts(events), "")
		if updateText == "" {
			t.Fatal("expected non-empty update text")
		}
		if count := strings.Count(updateText, "agent1"); count != 1 {
			t.Fatalf("agent1 update count = %d in %q, want 1", count, updateText)
		}
		if count := strings.Count(updateText, "agent2"); count != 1 {
			t.Fatalf("agent2 update count = %d in %q, want 1", count, updateText)
		}
		if count := strings.Count(updateText, "abc"); count != 4 {
			t.Fatalf("abc update count = %d in %q, want 4", count, updateText)
		}

		resultTexts := collectMessageTexts(collectOutputMessages(events))
		if len(resultTexts) != 2 {
			t.Fatalf("result texts = %v, want 2 messages", resultTexts)
		}
		for _, want := range []string{"agent1abcabc", "agent2abcabc"} {
			if !slices.Contains(resultTexts, want) {
				t.Fatalf("result texts = %v, missing %q", resultTexts, want)
			}
		}
	}
}
