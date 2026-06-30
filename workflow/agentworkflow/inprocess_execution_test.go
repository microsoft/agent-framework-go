// Copyright (c) Microsoft. All rights reserved.

package agentworkflow_test

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/agentworkflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func newEchoAgent(name string) *agent.Agent {
	const id = "echo-id"
	run := func(ctx context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			var lastUser string
			for _, m := range msgs {
				if m == nil || m.Role != message.RoleUser {
					continue
				}
				if t := m.Contents.Text(); t != "" {
					lastUser = t
				}
			}
			text := "Echo: no message"
			if lastUser != "" {
				text = "Echo: " + lastUser
			}
			messageID := fmt.Sprintf("msg-%s", name)
			if !yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AuthorName: name,
				MessageID:  messageID,
			}, nil) {
				return
			}
			yield(&agent.ResponseUpdate{
				Role:       message.RoleAssistant,
				AuthorName: name,
				MessageID:  messageID,
				Contents:   []message.Content{&message.TextContent{Text: text}},
			}, nil)
		}
	}
	return agent.New(
		agent.ProviderConfig{ProviderName: "echo", Run: run},
		agent.Config{
			ID:                  id,
			Name:                name,
			DisableFuncAutoCall: true,
		},
	)
}

func buildSequentialWorkflow(t *testing.T, a *agent.Agent) *workflow.Workflow {
	t.Helper()
	binding := agentworkflow.New(a, agentworkflow.Config{
		EmitUpdateEvents:   true,
		EmitResponseEvents: true,
	})
	wf, err := workflow.NewBuilder(binding).WithOutputFrom(binding).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return wf
}

func TestRunAsync_ExecutesWorkflow(t *testing.T) {
	a := newEchoAgent("test-agent")
	wf := buildSequentialWorkflow(t, a)

	ctx := context.Background()
	input := []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "Hello"}},
	}}
	run, err := inproc.Default.Run(ctx, wf, input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	status, err := run.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != inproc.RunStatusIdle {
		t.Errorf("status = %v, want Idle", status)
	}

	events := slices.Collect(run.OutgoingEvents())
	if len(events) == 0 {
		t.Fatalf("expected events, got 0")
	}
	if !containsOutputPayloadType[*agent.ResponseUpdate](events) {
		t.Errorf("expected at least one OutputEvent carrying *agent.ResponseUpdate")
	}
	if !containsOutputPayloadType[*agent.Response](events) {
		t.Errorf("expected at least one OutputEvent carrying *agent.Response")
	}
}

func TestWorkflowBuilderWithHostedAgentsFiltersOutputEventWithoutWithOutputFrom(t *testing.T) {
	agent1 := agentworkflow.New(newEchoAgent("agent-1"), agentworkflow.Config{})
	agent2 := agentworkflow.New(newEchoAgent("agent-2"), agentworkflow.Config{})
	wf, err := workflow.NewBuilder(agent1).
		AddEdge(agent1, agent2).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	events := runHostedAgentWorkflow(t, wf)
	if containsOutputPayloadType[*agent.ResponseUpdate](events) {
		t.Fatalf("expected hosted agent response updates to be filtered without WithOutputFrom")
	}
}

func TestWorkflowBuilderWithHostedAgentsTagsIntermediateOutputs(t *testing.T) {
	binding := agentworkflow.New(newEchoAgent("agent-1"), agentworkflow.Config{
		EmitResponseEvents: true,
	})
	wf, err := workflow.NewBuilder(binding).
		WithIntermediateOutputFrom(binding).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	events := runHostedAgentWorkflow(t, wf)
	updates := outputEventsWithPayloadType[*agent.ResponseUpdate](events)
	if len(updates) == 0 {
		t.Fatal("expected at least one OutputEvent carrying *agent.ResponseUpdate")
	}
	responses := outputEventsWithPayloadType[*agent.Response](events)
	if len(responses) == 0 {
		t.Fatal("expected at least one OutputEvent carrying *agent.Response")
	}
	for _, output := range append(updates, responses...) {
		if !output.IsIntermediate() {
			t.Fatalf("OutputEvent tags = %v, want intermediate", output.Tags)
		}
	}
}

func TestStreamAsync_ExecutesWorkflowWithTurnToken(t *testing.T) {
	a := newEchoAgent("test-agent")
	wf := buildSequentialWorkflow(t, a)

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "Hello"}},
	}})
	emit := true
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{EmitEvents: &emit})

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		events = append(events, evt)
	}

	status, err := stream.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != inproc.RunStatusIdle {
		t.Errorf("status = %v, want Idle", status)
	}
	if !containsOutputPayloadType[*agent.ResponseUpdate](events) {
		t.Errorf("expected at least one OutputEvent carrying *agent.ResponseUpdate")
	}
	if !containsOutputPayloadType[*agent.Response](events) {
		t.Errorf("expected at least one OutputEvent carrying *agent.Response")
	}
}

func runHostedAgentWorkflow(t *testing.T, wf *workflow.Workflow) []workflow.Event {
	t.Helper()
	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "Hello"}},
	}})
	emit := true
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{EmitEvents: &emit})

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		events = append(events, evt)
	}
	return events
}

func TestRunAsyncAndStreamAsync_ProduceSimilarResults(t *testing.T) {
	wf1 := buildSequentialWorkflow(t, newEchoAgent("test-agent-1"))
	wf2 := buildSequentialWorkflow(t, newEchoAgent("test-agent-2"))

	ctx := context.Background()
	input := func() []*message.Message {
		return []*message.Message{{
			Role:     message.RoleUser,
			Contents: []message.Content{&message.TextContent{Text: "Test message"}},
		}}
	}

	run, err := inproc.Default.Run(ctx, wf1, input())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	nonStreamingEvents := slices.Collect(run.OutgoingEvents())

	stream, err := inproc.Default.RunStreaming(ctx, wf2, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()
	sendStreamMessage(t, stream, ctx, input())
	emit := true
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{EmitEvents: &emit})
	var streamingEvents []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		streamingEvents = append(streamingEvents, evt)
	}

	if len(streamingEvents) == 0 {
		t.Fatalf("streaming version produced no events")
	}
	if len(nonStreamingEvents) == 0 {
		t.Fatalf("non-streaming version produced no events")
	}
	if got, want := countOutputPayloadType[*agent.ResponseUpdate](nonStreamingEvents), countOutputPayloadType[*agent.ResponseUpdate](streamingEvents); got != want {
		t.Errorf("agent update count: non-streaming=%d, streaming=%d", got, want)
	}
}

func TestRunStreamingAsync_StatusReachesIdleBeforeWatch(t *testing.T) {
	a := newEchoAgent("test-agent")
	wf := buildSequentialWorkflow(t, a)

	ctx := context.Background()
	stream, err := inproc.Default.RunStreaming(ctx, wf, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.CancelRun() }()

	sendStreamMessage(t, stream, ctx, []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "Hello"}},
	}})
	emit := true
	sendStreamMessage(t, stream, ctx, workflow.TurnToken{EmitEvents: &emit})

	deadline := 200
	for deadline > 0 {
		status, err := stream.GetStatus(ctx)
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if status == inproc.RunStatusIdle {
			break
		}
		time.Sleep(5 * time.Millisecond)
		deadline--
	}

	var events []workflow.Event
	for evt, err := range stream.WatchStream(ctx) {
		if err != nil {
			t.Fatalf("watch: %v", err)
		}
		events = append(events, evt)
	}

	status, err := stream.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != inproc.RunStatusIdle {
		t.Errorf("status = %v, want Idle", status)
	}
	if len(events) == 0 {
		t.Fatalf("expected events even after run reached Idle, got 0")
	}
	if !containsOutputPayloadType[*agent.ResponseUpdate](events) {
		t.Errorf("expected at least one OutputEvent carrying *agent.ResponseUpdate")
	}
}

// containsOutputPayloadType reports whether any OutputEvent has payload type T.
func containsOutputPayloadType[T any](events []workflow.Event) bool {
	return countOutputPayloadType[T](events) > 0
}

// countOutputPayloadType returns the number of OutputEvents with payload type T.
func countOutputPayloadType[T any](events []workflow.Event) int {
	n := 0
	for _, e := range events {
		out, ok := e.(workflow.OutputEvent)
		if !ok {
			continue
		}
		if _, ok := out.Output.(T); ok {
			n++
		}
	}
	return n
}

func outputEventsWithPayloadType[T any](events []workflow.Event) []workflow.OutputEvent {
	var outputs []workflow.OutputEvent
	for _, e := range events {
		out, ok := e.(workflow.OutputEvent)
		if !ok {
			continue
		}
		if _, ok := out.Output.(T); ok {
			outputs = append(outputs, out)
		}
	}
	return outputs
}
