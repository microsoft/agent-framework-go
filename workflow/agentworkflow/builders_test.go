// Copyright (c) Microsoft. All rights reserved.

package agentworkflow_test

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func TestBuildSequential_RejectsInvalidArguments(t *testing.T) {
	for _, agents := range [][]*agent.Agent{nil, {}} {
		if _, err := agentworkflow.NewSequentialWorkflowBuilder(agents...).Build(); err == nil {
			t.Fatal("expected error for null or empty agents")
		}
	}
}

func TestBuildConcurrent_RejectsNilAgents(t *testing.T) {
	var agents []*agent.Agent
	if _, err := agentworkflow.NewConcurrentWorkflowBuilder(agents...).Build(); err == nil {
		t.Fatal("expected error for null agents")
	}
}

func TestBuildSequential_DelegatesToBuilder(t *testing.T) {
	for _, count := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var agents []*agent.Agent
			for i := 1; i <= count; i++ {
				agents = append(agents, newDoubleEchoAgent(fmt.Sprintf("agent%d", i)))
			}
			wf, err := agentworkflow.NewSequentialWorkflowBuilder(agents...).Build()
			if err != nil {
				t.Fatal(err)
			}
			events := runBuiltWorkflowWithText(t, wf, "abc")
			if got := collectOutputMessages(events); len(got) != count+1 {
				t.Fatalf("result = %v, want %d messages", got, count+1)
			}
			if strings.Join(collectOutputTexts(events), "") == "" {
				t.Fatal("expected nonempty update text")
			}
		})
	}
}

func TestBuildSequential_ChainOnlyAgentResponses(t *testing.T) {
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(newDoubleEchoAgent("agent1"), newDoubleEchoAgent("agent2")).
		WithChainOnlyAgentResponses(true).Build()
	if err != nil {
		t.Fatal(err)
	}
	result := collectOutputMessages(runBuiltWorkflowWithText(t, wf, "abc"))
	if len(result) != 1 || result[0].AuthorName != "agent2" {
		t.Fatalf("result = %v, want one message authored by agent2", result)
	}
}

func TestBuildSequential_WorkflowName(t *testing.T) {
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(newDoubleEchoAgent("agent1")).WithName("static-sequential").Build()
	if err != nil || wf.Name() != "static-sequential" {
		t.Fatalf("workflow = %v, error = %v", wf, err)
	}
}

func TestBuildConcurrent_WorkflowName(t *testing.T) {
	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(newDoubleEchoAgent("agent1")).WithName("static-concurrent").Build()
	if err != nil || wf.Name() != "static-concurrent" {
		t.Fatalf("workflow = %v, error = %v", wf, err)
	}
}

func TestBuildConcurrent_DelegatesToBuilder(t *testing.T) {
	barrier := &runBarrier{}
	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(
		newBarrierDoubleEchoAgent("agent1", barrier), newBarrierDoubleEchoAgent("agent2", barrier),
	).Build()
	if err != nil {
		t.Fatal(err)
	}
	barrier.reset(2)
	events := runBuiltWorkflowWithText(t, wf, "abc")
	text := strings.Join(collectOutputTexts(events), "")
	if text == "" || strings.Count(text, "agent1") != 1 || strings.Count(text, "agent2") != 1 {
		t.Fatalf("update text = %q, want one update from each participant", text)
	}
	if result := collectOutputMessages(events); len(result) != 2 {
		t.Fatalf("result = %v, want two messages", result)
	}
}

func TestBuildConcurrent_HonorsAggregator(t *testing.T) {
	sentinel := []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "custom-aggregator-result"}}}}
	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(newDoubleEchoAgent("agent1")).
		WithAggregator(func(context.Context, [][]*message.Message) []*message.Message { return sentinel }).Build()
	if err != nil {
		t.Fatal(err)
	}
	result := collectOutputMessages(runBuiltWorkflowWithText(t, wf, "abc"))
	if len(result) != 1 || result[0].Contents.Text() != "custom-aggregator-result" {
		t.Fatalf("result = %v, want custom-aggregator-result", result)
	}
}

func TestSequentialBuilderFactory_ReturnsConfigurableBuilder(t *testing.T) {
	builder := agentworkflow.NewSequentialWorkflowBuilder(newDoubleEchoAgent("agent1"))
	wf, err := builder.WithName("via-factory").Build()
	if err != nil || wf.Name() != "via-factory" {
		t.Fatalf("workflow = %v, error = %v", wf, err)
	}
}

func TestConcurrentBuilderFactory_ReturnsConfigurableBuilder(t *testing.T) {
	builder := agentworkflow.NewConcurrentWorkflowBuilder(newDoubleEchoAgent("agent1"))
	wf, err := builder.WithName("via-factory").Build()
	if err != nil || wf.Name() != "via-factory" {
		t.Fatalf("workflow = %v, error = %v", wf, err)
	}
}

func TestSequentialWorkflowBuilder_DefaultDesignations(t *testing.T) {
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(newDoubleEchoAgent("agent1"), newDoubleEchoAgent("agent2"), newDoubleEchoAgent("agent3")).Build()
	if err != nil {
		t.Fatal(err)
	}
	assertDesignationCounts(t, wf, 1, 3)
}

func TestConcurrentWorkflowBuilder_DefaultDesignations(t *testing.T) {
	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(newDoubleEchoAgent("agent1"), newDoubleEchoAgent("agent2"), newDoubleEchoAgent("agent3")).Build()
	if err != nil {
		t.Fatal(err)
	}
	assertDesignationCounts(t, wf, 1, 6)
}

func TestSequentialWorkflowBuilder_ExplicitDesignationsReplaceDefaults(t *testing.T) {
	a1, a2, a3 := newDoubleEchoAgent("agent1"), newDoubleEchoAgent("agent2"), newDoubleEchoAgent("agent3")
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(a1, a2, a3).WithOutputFrom(a1).WithIntermediateOutputFrom(a2).Build()
	if err != nil {
		t.Fatal(err)
	}
	assertDesignationCounts(t, wf, 1, 1)
}

func TestConcurrentWorkflowBuilder_ExplicitDesignationsReplaceDefaults(t *testing.T) {
	a1, a2, a3 := newDoubleEchoAgent("agent1"), newDoubleEchoAgent("agent2"), newDoubleEchoAgent("agent3")
	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(a1, a2, a3).WithOutputFrom(a1).WithIntermediateOutputFrom(a2).Build()
	if err != nil {
		t.Fatal(err)
	}
	assertDesignationCounts(t, wf, 1, 1)
}

func assertDesignationCounts(t *testing.T, wf *workflow.Workflow, terminal, intermediate int) {
	t.Helper()
	gotTerminal, gotIntermediate := 0, 0
	for _, tags := range wf.OutputExecutors() {
		if len(tags) == 0 {
			gotTerminal++
		}
		if slices.Contains(tags, workflow.OutputTagIntermediate) {
			gotIntermediate++
		}
	}
	if gotTerminal != terminal || gotIntermediate != intermediate || len(wf.OutputExecutors()) != terminal+intermediate {
		t.Fatalf("designations = %v, want %d terminal, %d intermediate", wf.OutputExecutors(), terminal, intermediate)
	}
}

func TestSequentialWorkflowBuilder_Description(t *testing.T) {
	const description = "describes the sequential pipeline"
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(newDoubleEchoAgent("agent1")).WithDescription(description).Build()
	if err != nil || wf.Description() != description {
		t.Fatalf("workflow = %v, error = %v", wf, err)
	}
}

func TestConcurrentWorkflowBuilder_Description(t *testing.T) {
	const description = "describes the concurrent fan-out/fan-in"
	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(newDoubleEchoAgent("agent1")).WithDescription(description).Build()
	if err != nil || wf.Description() != description {
		t.Fatalf("workflow = %v, error = %v", wf, err)
	}
}

func TestSequentialWorkflowBuilder_NextAgentReceivesFullConversation(t *testing.T) {
	var seen []*message.Message
	first := newLabeledEchoAgent("agent1", "agent1", "step-one")
	second := newCapturingReplyAgent("agent2", "step-two", &seen)
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(first, second).Build()
	if err != nil {
		t.Fatal(err)
	}
	runBuiltWorkflowWithText(t, wf, "start")
	if len(seen) != 2 || seen[0].Role != message.RoleUser || seen[0].Contents.Text() != "start" ||
		seen[1].Role != message.RoleUser || seen[1].Contents.Text() != "step-one" {
		t.Fatalf("second agent input = %v, want user start and user step-one", seen)
	}
}

func TestSequentialWorkflowBuilder_ChainOnlyNextAgentReceivesPreviousOutput(t *testing.T) {
	var seen []*message.Message
	first := newLabeledEchoAgent("agent1", "agent1", "step-one")
	second := newCapturingReplyAgent("agent2", "step-two", &seen)
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(first, second).WithChainOnlyAgentResponses(true).Build()
	if err != nil {
		t.Fatal(err)
	}
	runBuiltWorkflowWithText(t, wf, "start")
	if len(seen) != 1 || seen[0].Role != message.RoleUser || seen[0].Contents.Text() != "step-one" {
		t.Fatalf("second agent input = %v, want only user step-one", seen)
	}
}

func newCapturingReplyAgent(name, reply string, seen *[]*message.Message) *agent.Agent {
	return agent.New(agent.ProviderConfig{
		ProviderName: "capture-reply",
		Run: func(_ context.Context, messages []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			*seen = slices.Clone(messages)
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				yield(&agent.ResponseUpdate{Role: message.RoleAssistant, AuthorName: name, Contents: message.Contents{&message.TextContent{Text: reply}}}, nil)
			}
		},
	}, agent.Config{ID: name, Name: name})
}

func TestConcurrentWorkflowBuilder_AsAgentOnlyTerminalDesignationSurfaces(t *testing.T) {
	a1, a2 := newDoubleEchoAgent("agent1"), newDoubleEchoAgent("agent2")
	wf, err := agentworkflow.NewConcurrentWorkflowBuilder(a1, a2).WithOutputFrom(a1).Build()
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyFirstParticipantSurfaces(t, wf)
}

func TestSequentialWorkflowBuilder_AsAgentOnlyTerminalDesignationSurfaces(t *testing.T) {
	a1, a2, a3 := newDoubleEchoAgent("agent1"), newDoubleEchoAgent("agent2"), newDoubleEchoAgent("agent3")
	wf, err := agentworkflow.NewSequentialWorkflowBuilder(a1, a2, a3).WithOutputFrom(a3).Build()
	if err != nil {
		t.Fatal(err)
	}
	a, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{Config: agent.Config{ID: "WorkflowAgent"}})
	if err != nil {
		t.Fatal(err)
	}
	authors := map[string]bool{}
	for update, err := range a.Run(t.Context(), []*message.Message{message.NewText("abc")}, agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		authors[update.AuthorName] = true
	}
	if !authors["agent3"] || authors["agent1"] || authors["agent2"] {
		t.Fatalf("authors = %v, want only agent3", authors)
	}
}

func assertOnlyFirstParticipantSurfaces(t *testing.T, wf *workflow.Workflow) {
	t.Helper()
	a, err := agentworkflow.NewAgent(wf, agentworkflow.AgentConfig{Config: agent.Config{ID: "WorkflowAgent"}})
	if err != nil {
		t.Fatal(err)
	}
	authors := map[string]bool{}
	for update, err := range a.Run(t.Context(), []*message.Message{message.NewText("abc")}, agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		authors[update.AuthorName] = true
	}
	if !authors["agent1"] || authors["agent2"] {
		t.Fatalf("authors = %v, want agent1 and not agent2", authors)
	}
}

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
		agent.Config{ID: id, Name: name},
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
		agent.Config{ID: id, Name: id},
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
		agent.Config{ID: id, Name: id},
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
	ctx := t.Context()
	stream, err := inproc.Lockstep.OpenStreaming(ctx, wf)
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
	if _, err := stream.TrySendMessage(ctx, userMsg); err != nil {
		t.Fatalf("SendMessage user msg: %v", err)
	}
	emitEvents := true
	if _, err := stream.TrySendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
		t.Fatalf("SendMessage turn token: %v", err)
	}

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("WatchStream: %v", err)
		}
		switch evt := evt.(type) {
		case workflow.ErrorEvent:
			t.Fatalf("unexpected workflow error: %v", evt.Error)
		case workflow.ExecutorFailedEvent:
			t.Fatalf("unexpected executor failure from %q: %v", evt.ExecutorID, evt.Error)
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
	if _, err := stream.TrySendMessage(ctx, userMsg); err != nil {
		t.Fatalf("SendMessage user msg: %v", err)
	}
	emitEvents := true
	if _, err := stream.TrySendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
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

func outputExecutorTags(wf *workflow.Workflow, executorID string) []workflow.OutputTag {
	if wf == nil {
		return nil
	}
	tags, ok := wf.OutputExecutors()[executorID]
	if !ok {
		return nil
	}
	return tags
}
