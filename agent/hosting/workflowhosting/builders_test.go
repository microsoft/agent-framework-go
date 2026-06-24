// Copyright (c) Microsoft. All rights reserved.

package workflowhosting_test

import (
	"context"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
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
	emitEvents := true
	if err := stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
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
	emitEvents := true
	if err := stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emitEvents}); err != nil {
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
