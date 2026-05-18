// Copyright (c) Microsoft. All rights reserved.

package workflowhosting_test

import (
	"context"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

// newLabeledEchoAgent returns a deterministic agent that emits a single text
// update with the given label regardless of its input.
func newLabeledEchoAgent(id, name, label string) *agent.Agent {
	run := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: name,
				Contents:   []message.Content{&message.TextContent{Text: label}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "echo", Run: run},
		agent.Config{ID: id, Name: name, DisableFuncAutoCall: true},
	)
}

// runBuiltWorkflow is a test helper that runs a pre-built workflow for one
// turn and collects all emitted events.
func runBuiltWorkflow(t *testing.T, wf *workflow.Workflow) []workflow.Event {
	t.Helper()
	ctx := context.Background()
	stream, err := inproc.Lockstep.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("RunStreaming: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	userMsg := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hello"}}},
	}
	if err := stream.SendMessage(ctx, userMsg); err != nil {
		t.Fatalf("SendMessage user msg: %v", err)
	}
	if err := stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: boolPtr(true)}); err != nil {
		t.Fatalf("SendMessage turn token: %v", err)
	}

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("WatchStream: %v", err)
		}
		events = append(events, evt)
	}
	return events
}

// collectOutputTexts returns the text labels emitted as OutputEvents carrying
// *agent.ResponseUpdate payloads.
func collectOutputTexts(events []workflow.Event) []string {
	var texts []string
	for _, evt := range events {
		out, ok := evt.(workflow.OutputEvent)
		if !ok {
			continue
		}
		upd, ok := out.Output.(*agent.ResponseUpdate)
		if !ok {
			continue
		}
		for _, c := range upd.Contents {
			if tc, ok := c.(*message.TextContent); ok {
				texts = append(texts, tc.Text)
			}
		}
	}
	return texts
}

// TestBuildSequential_ReturnsErrorForNoAgents checks that BuildSequential
// rejects an empty agent list.
func TestBuildSequential_ReturnsErrorForNoAgents(t *testing.T) {
	_, err := workflowhosting.BuildSequential()
	if err == nil {
		t.Fatal("expected error for zero agents, got nil")
	}
}

// TestBuildConcurrent_ReturnsErrorForNoAgents checks that BuildConcurrent
// rejects an empty agent list.
func TestBuildConcurrent_ReturnsErrorForNoAgents(t *testing.T) {
	_, err := workflowhosting.BuildConcurrent()
	if err == nil {
		t.Fatal("expected error for zero agents, got nil")
	}
}

// TestBuildSequential_SingleAgent verifies that a single-agent sequential
// workflow builds successfully and emits the agent's output.
func TestBuildSequential_SingleAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	wf, err := workflowhosting.BuildSequential(a)
	if err != nil {
		t.Fatalf("BuildSequential: %v", err)
	}

	texts := collectOutputTexts(runBuiltWorkflow(t, wf))
	if len(texts) != 1 || texts[0] != "from-a" {
		t.Errorf("got texts %v, want [from-a]", texts)
	}
}

// TestBuildSequential_MultiAgent verifies that a multi-agent sequential
// workflow builds successfully and that the last agent's output is emitted.
func TestBuildSequential_MultiAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	c := newLabeledEchoAgent("c", "C", "from-c")
	wf, err := workflowhosting.BuildSequential(a, b, c)
	if err != nil {
		t.Fatalf("BuildSequential: %v", err)
	}

	texts := collectOutputTexts(runBuiltWorkflow(t, wf))
	// Only the last agent (c) is the output node, so we expect "from-c".
	if len(texts) == 0 {
		t.Fatal("expected at least one output text")
	}
	last := texts[len(texts)-1]
	if last != "from-c" {
		t.Errorf("last output text = %q, want %q", last, "from-c")
	}
}

// TestBuildConcurrent_SingleAgent verifies that a single-agent concurrent
// workflow builds and emits the agent's output.
func TestBuildConcurrent_SingleAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	wf, err := workflowhosting.BuildConcurrent(a)
	if err != nil {
		t.Fatalf("BuildConcurrent: %v", err)
	}

	texts := collectOutputTexts(runBuiltWorkflow(t, wf))
	if len(texts) != 1 || texts[0] != "from-a" {
		t.Errorf("got texts %v, want [from-a]", texts)
	}
}

// TestBuildConcurrent_MultiAgent verifies that a multi-agent concurrent
// workflow builds and emits output from all agents.
func TestBuildConcurrent_MultiAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	wf, err := workflowhosting.BuildConcurrent(a, b)
	if err != nil {
		t.Fatalf("BuildConcurrent: %v", err)
	}

	texts := collectOutputTexts(runBuiltWorkflow(t, wf))

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
}
