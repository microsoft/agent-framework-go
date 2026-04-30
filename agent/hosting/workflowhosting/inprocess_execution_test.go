// Copyright (c) Microsoft. All rights reserved.

package workflowhosting_test

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/workflowhosting"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

func newEchoAgent(name string) *agent.Agent {
	const id = "echo-id"
	run := func(ctx context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
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
			if !yield(&message.ResponseUpdate{
				Role:       message.RoleAssistant,
				AuthorName: name,
				MessageID:  messageID,
			}, nil) {
				return
			}
			yield(&message.ResponseUpdate{
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
	binding := workflowhosting.New(a, workflowhosting.Config{
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
	run, err := inproc.Run(ctx, wf, "", input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	status, err := run.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != workflow.RunStatusIdle {
		t.Errorf("status = %v, want Idle", status)
	}

	events := slices.Collect(run.OutgoingEvents())
	if len(events) == 0 {
		t.Fatalf("expected events, got 0")
	}
	if !containsType[workflow.ResponseUpdateEvent](events) {
		t.Errorf("expected at least one ResponseUpdateEvent")
	}
	if !containsType[workflow.ResponseEvent](events) {
		t.Errorf("expected at least one ResponseEvent")
	}
}

func TestStreamAsync_ExecutesWorkflowWithTurnToken(t *testing.T) {
	a := newEchoAgent("test-agent")
	wf := buildSequentialWorkflow(t, a)

	ctx := context.Background()
	stream, err := inproc.OpenStream(ctx, wf, "")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer stream.Cancel()

	if err := stream.SendMessage(ctx, []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "Hello"}},
	}}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	emit := true
	if err := stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emit}); err != nil {
		t.Fatalf("SendMessage(TurnToken): %v", err)
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
	if status != workflow.RunStatusIdle {
		t.Errorf("status = %v, want Idle", status)
	}
	if !containsType[workflow.ResponseUpdateEvent](events) {
		t.Errorf("expected at least one ResponseUpdateEvent")
	}
	if !containsType[workflow.ResponseEvent](events) {
		t.Errorf("expected at least one ResponseEvent")
	}
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

	run, err := inproc.Run(ctx, wf1, "", input())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	nonStreamingEvents := slices.Collect(run.OutgoingEvents())

	stream, err := inproc.OpenStream(ctx, wf2, "")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer stream.Cancel()
	if err := stream.SendMessage(ctx, input()); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	emit := true
	if err := stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emit}); err != nil {
		t.Fatalf("SendMessage(TurnToken): %v", err)
	}
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
	if got, want := countType[workflow.ResponseUpdateEvent](nonStreamingEvents), countType[workflow.ResponseUpdateEvent](streamingEvents); got != want {
		t.Errorf("agent update count: non-streaming=%d, streaming=%d", got, want)
	}
}

func TestRunStreamingAsync_StatusReachesIdleBeforeWatch(t *testing.T) {
	a := newEchoAgent("test-agent")
	wf := buildSequentialWorkflow(t, a)

	ctx := context.Background()
	stream, err := inproc.OpenStream(ctx, wf, "")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer stream.Cancel()

	if err := stream.SendMessage(ctx, []*message.Message{{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: "Hello"}},
	}}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	emit := true
	if err := stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emit}); err != nil {
		t.Fatalf("SendMessage(TurnToken): %v", err)
	}

	deadline := 200
	for deadline > 0 {
		status, err := stream.GetStatus(ctx)
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if status == workflow.RunStatusIdle {
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
	if status != workflow.RunStatusIdle {
		t.Errorf("status = %v, want Idle", status)
	}
	if len(events) == 0 {
		t.Fatalf("expected events even after run reached Idle, got 0")
	}
	if !containsType[workflow.ResponseUpdateEvent](events) {
		t.Errorf("expected at least one ResponseUpdateEvent")
	}
}

// containsType reports whether any element of events is of type T.
func containsType[T any](events []workflow.Event) bool {
	for _, e := range events {
		if _, ok := e.(T); ok {
			return true
		}
	}
	return false
}

// countType returns the number of events of type T in events.
func countType[T any](events []workflow.Event) int {
	n := 0
	for _, e := range events {
		if _, ok := e.(T); ok {
			n++
		}
	}
	return n
}
