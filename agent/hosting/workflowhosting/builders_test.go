// Copyright (c) Microsoft. All rights reserved.

package workflowhosting_test

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

func newDoubleEchoAgent(id string) *agent.Agent {
	run := func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			inputText := concatenateMessageText(messages)
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: id,
				Contents: []message.Content{
					&message.TextContent{Text: id + inputText + inputText},
				},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "double-echo", Run: run},
		agent.Config{ID: id, Name: id, DisableFuncAutoCall: true},
	)
}

type runBarrier struct {
	mu        sync.Mutex
	remaining int
	ready     chan struct{}
}

func (b *runBarrier) reset(count int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.remaining = count
	b.ready = make(chan struct{})
}

func (b *runBarrier) wait(ctx context.Context) error {
	b.mu.Lock()
	ready := b.ready
	b.remaining--
	if b.remaining == 0 {
		close(ready)
	}
	b.mu.Unlock()

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newBarrierDoubleEchoAgent(id string, barrier *runBarrier) *agent.Agent {
	run := func(ctx context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if err := barrier.wait(ctx); err != nil {
				yield(nil, err)
				return
			}
			inputText := concatenateMessageText(messages)
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AgentID:    id,
				AuthorName: id,
				Contents: []message.Content{
					&message.TextContent{Text: id + inputText + inputText},
				},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "barrier-double-echo", Run: run},
		agent.Config{ID: id, Name: id, DisableFuncAutoCall: true},
	)
}

func concatenateMessageText(messages []*message.Message) string {
	var builder strings.Builder
	for _, msg := range messages {
		for _, content := range msg.Contents {
			if textContent, ok := content.(*message.TextContent); ok {
				builder.WriteString(textContent.Text)
			}
		}
	}
	return builder.String()
}

// runBuiltWorkflow is a test helper that runs a pre-built workflow for one
// turn and collects all emitted events.
func runBuiltWorkflow(t *testing.T, wf *workflow.Workflow) []workflow.Event {
	t.Helper()
	return runBuiltWorkflowWithText(t, wf, "hello")
}

func runBuiltWorkflowWithText(t *testing.T, wf *workflow.Workflow, inputText string) []workflow.Event {
	t.Helper()
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

	userMsg := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: inputText}}},
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

func runStreamingWorkflowTurn(t *testing.T, ctx context.Context, stream *inproc.StreamingRun, inputText string) []workflow.Event {
	t.Helper()
	userMsg := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: inputText}}},
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

func collectOutputMessages(events []workflow.Event) []*message.Message {
	var messages []*message.Message
	for _, evt := range events {
		out, ok := evt.(workflow.OutputEvent)
		if !ok {
			continue
		}
		if outputMessages, ok := out.Output.([]*message.Message); ok {
			messages = outputMessages
		}
	}
	return messages
}

func collectMessageTexts(messages []*message.Message) []string {
	texts := make([]string, 0, len(messages))
	for _, msg := range messages {
		for _, content := range msg.Contents {
			if textContent, ok := content.(*message.TextContent); ok {
				texts = append(texts, textContent.Text)
			}
		}
	}
	return texts
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
	_, err := workflowhosting.BuildSequential("")
	if err == nil {
		t.Fatal("expected error for zero agents, got nil")
	}
}

func TestBuildSequential_ReturnsErrorForNilAgent(t *testing.T) {
	validAgent := newLabeledEchoAgent("a", "A", "from-a")
	for _, tt := range []struct {
		name   string
		agents []*agent.Agent
	}{
		{name: "only_nil", agents: []*agent.Agent{nil}},
		{name: "second_nil", agents: []*agent.Agent{validAgent, nil}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := workflowhosting.BuildSequential("", tt.agents...)
			if err == nil {
				t.Fatal("expected error for nil agent, got nil")
			}
			if !strings.Contains(err.Error(), "nil") {
				t.Fatalf("error = %q, want it to mention nil", err.Error())
			}
		})
	}
}

// TestBuildConcurrent_ReturnsErrorForNoAgents checks that BuildConcurrent
// rejects an empty agent list.
func TestBuildConcurrent_ReturnsErrorForNoAgents(t *testing.T) {
	_, err := workflowhosting.BuildConcurrent("")
	if err == nil {
		t.Fatal("expected error for zero agents, got nil")
	}
}

func TestBuildConcurrent_ReturnsErrorForNilAgent(t *testing.T) {
	validAgent := newLabeledEchoAgent("a", "A", "from-a")
	for _, tt := range []struct {
		name   string
		agents []*agent.Agent
	}{
		{name: "only_nil", agents: []*agent.Agent{nil}},
		{name: "second_nil", agents: []*agent.Agent{validAgent, nil}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := workflowhosting.BuildConcurrent("", tt.agents...)
			if err == nil {
				t.Fatal("expected error for nil agent, got nil")
			}
			if !strings.Contains(err.Error(), "nil") {
				t.Fatalf("error = %q, want it to mention nil", err.Error())
			}
		})
	}
}

// TestBuildSequential_SingleAgent verifies that a single-agent sequential
// workflow builds successfully and emits the agent's output.
func TestBuildSequential_SingleAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	wf, err := workflowhosting.BuildSequential("single-agent", a)
	if err != nil {
		t.Fatalf("BuildSequential: %v", err)
	}
	if wf.Name != "single-agent" {
		t.Fatalf("workflow name = %q, want %q", wf.Name, "single-agent")
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
	wf, err := workflowhosting.BuildSequential("", a, b, c)
	if err != nil {
		t.Fatalf("BuildSequential: %v", err)
	}
	if wf.Name != "" {
		t.Fatalf("workflow name = %q, want empty", wf.Name)
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

// TestBuildSequential_AgentsRunInOrder verifies that each agent receives the
// accumulated conversation produced by the agents before it.
func TestBuildSequential_AgentsRunInOrder(t *testing.T) {
	for _, numAgents := range []int{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("%d_agents", numAgents), func(t *testing.T) {
			agents := make([]*agent.Agent, 0, numAgents)
			for agentNumber := 1; agentNumber <= numAgents; agentNumber++ {
				agents = append(agents, newDoubleEchoAgent(fmt.Sprintf("agent%d", agentNumber)))
			}

			wf, err := workflowhosting.BuildSequential("", agents...)
			if err != nil {
				t.Fatalf("BuildSequential: %v", err)
			}

			for range 3 {
				const inputText = "abc"
				events := runBuiltWorkflowWithText(t, wf, inputText)
				texts := collectOutputTexts(events)
				want := expectedSequentialDoubleEchoOutputs(numAgents, inputText)
				if !slices.Equal(texts, want) {
					t.Fatalf("output texts = %v, want %v", texts, want)
				}

				resultMessages := collectOutputMessages(events)
				wantResultTexts := append([]string{inputText}, want...)
				if gotResultTexts := collectMessageTexts(resultMessages); !slices.Equal(gotResultTexts, wantResultTexts) {
					t.Fatalf("result texts = %v, want %v", gotResultTexts, wantResultTexts)
				}
				if len(resultMessages) != numAgents+1 {
					t.Fatalf("result count = %d, want %d", len(resultMessages), numAgents+1)
				}
				if resultMessages[0].Role != message.RoleUser {
					t.Fatalf("result[0].Role = %q, want %q", resultMessages[0].Role, message.RoleUser)
				}
				for resultIndex, resultMessage := range resultMessages[1:] {
					wantAuthorName := fmt.Sprintf("agent%d", resultIndex+1)
					if resultMessage.Role != message.RoleAssistant {
						t.Fatalf("result[%d].Role = %q, want %q", resultIndex+1, resultMessage.Role, message.RoleAssistant)
					}
					if resultMessage.AuthorName != wantAuthorName {
						t.Fatalf("result[%d].AuthorName = %q, want %q", resultIndex+1, resultMessage.AuthorName, wantAuthorName)
					}
				}
			}
		})
	}
}

func expectedSequentialDoubleEchoOutputs(numAgents int, inputText string) []string {
	transcript := inputText
	outputs := make([]string, 0, numAgents)
	for agentNumber := 1; agentNumber <= numAgents; agentNumber++ {
		agentID := fmt.Sprintf("agent%d", agentNumber)
		outputText := agentID + transcript + transcript
		outputs = append(outputs, outputText)
		transcript += outputText
	}
	return outputs
}

// TestBuildConcurrent_SingleAgent verifies that a single-agent concurrent
// workflow builds and emits the agent's output.
func TestBuildConcurrent_SingleAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	wf, err := workflowhosting.BuildConcurrent("single-concurrent", a)
	if err != nil {
		t.Fatalf("BuildConcurrent: %v", err)
	}
	if wf.Name != "single-concurrent" {
		t.Fatalf("workflow name = %q, want %q", wf.Name, "single-concurrent")
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

// TestBuildConcurrent_MultiAgent verifies that a multi-agent concurrent
// workflow builds and emits output from all agents.
func TestBuildConcurrent_MultiAgent(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	wf, err := workflowhosting.BuildConcurrent("", a, b)
	if err != nil {
		t.Fatalf("BuildConcurrent: %v", err)
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

func TestBuildConcurrentWithAggregator_UsesCustomAggregator(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	contextWasPresent := false
	wf, err := workflowhosting.BuildConcurrentWithAggregator("", func(ctx context.Context, lists [][]*message.Message) []*message.Message {
		contextWasPresent = ctx != nil
		return []*message.Message{{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.TextContent{Text: fmt.Sprintf("batches:%d", len(lists))},
			},
		}}
	}, a, b)
	if err != nil {
		t.Fatalf("BuildConcurrentWithAggregator: %v", err)
	}

	resultTexts := collectMessageTexts(collectOutputMessages(runBuiltWorkflow(t, wf)))
	if !slices.Equal(resultTexts, []string{"batches:2"}) {
		t.Fatalf("result texts = %v, want [batches:2]", resultTexts)
	}
	if !contextWasPresent {
		t.Fatal("expected aggregator to receive a context")
	}
}

func TestBuildConcurrentWithAggregator_ResetsEndExecutorBetweenTurns(t *testing.T) {
	a := newLabeledEchoAgent("a", "A", "from-a")
	b := newLabeledEchoAgent("b", "B", "from-b")
	callCount := 0
	wf, err := workflowhosting.BuildConcurrentWithAggregator("", func(_ context.Context, lists [][]*message.Message) []*message.Message {
		callCount++
		return []*message.Message{{
			Role: message.RoleAssistant,
			Contents: []message.Content{
				&message.TextContent{Text: fmt.Sprintf("turn:%d batches:%d", callCount, len(lists))},
			},
		}}
	}, a, b)
	if err != nil {
		t.Fatalf("BuildConcurrentWithAggregator: %v", err)
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

func TestBuildConcurrent_AgentsRunInParallel(t *testing.T) {
	barrier := &runBarrier{}
	wf, err := workflowhosting.BuildConcurrent("",
		newBarrierDoubleEchoAgent("agent1", barrier),
		newBarrierDoubleEchoAgent("agent2", barrier),
	)
	if err != nil {
		t.Fatalf("BuildConcurrent: %v", err)
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
