// Copyright (c) Microsoft. All rights reserved.

package toolautocall_test

import (
	"context"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// TestMessageInjection_ToolInjectsMessageDuringFunctionLoop verifies that when
// EnableMessageInjection is true and a tool enqueues a message via
// GetMessageInjector, the injected message is sent to the provider on the next
// service call without an explicit new user turn.
func TestMessageInjection_ToolInjectsMessageDuringFunctionLoop(t *testing.T) {
	type EmptyArgs struct{}

	const injectedText = "injected follow-up from tool"
	var secondTurnMsgs []*message.Message

	rb := agenttest.NewResponseBuilder().
		// Turn 0: provider asks to call the "Injecting" tool.
		AddFunctionCall("call1", "Injecting", `{}`).
		// Turn 1: provider receives the tool result + injected user message, returns final text.
		NewTurn(func(_ context.Context, msgs []*message.Message, _ ...agent.Option) {
			secondTurnMsgs = slices.Clone(msgs)
		}).
		AddText("Final answer after injection")

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Injecting", Description: "Injects a message"},
			func(ctx tool.Context, _ EmptyArgs) (string, error) {
				if mi := toolautocall.GetMessageInjector(ctx); mi != nil {
					mi.EnqueueMessages(message.New(&message.TextContent{Text: injectedText}))
				}
				return "tool done", nil
			}),
	}

	runner := &agenttest.Runner{Responses: rb.Build()}

	var opts []agent.Option
	for _, tl := range tools {
		opts = append(opts, agent.WithTool(tl))
	}

	initialMessages := []*message.Message{message.NewText("start")}
	var resp agent.Response
	for update, err := range toolautocall.New(toolautocall.Config{
		EnableMessageInjection: true,
		NewID:                  func() string { return "" },
	}).Run(runner.Run, t.Context(), initialMessages, opts...) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Update(update)
	}

	// The final text update should be present.
	foundFinal := slices.ContainsFunc(resp.Messages, func(m *message.Message) bool {
		return slices.ContainsFunc(m.Contents, func(c message.Content) bool {
			tc, ok := c.(*message.TextContent)
			return ok && tc.Text == "Final answer after injection"
		})
	})
	if !foundFinal {
		t.Fatal("expected final text response not found in updates")
	}

	// The injected message must have been forwarded to the second provider call.
	foundInjected := slices.ContainsFunc(secondTurnMsgs, func(m *message.Message) bool {
		return slices.ContainsFunc(m.Contents, func(c message.Content) bool {
			tc, ok := c.(*message.TextContent)
			return ok && tc.Text == injectedText
		})
	})
	if !foundInjected {
		t.Fatalf("injected message %q not found in second provider call messages", injectedText)
	}
}

// TestMessageInjection_DisabledWhenNotConfigured verifies that GetMessageInjector
// returns nil when EnableMessageInjection is false, preserving the default behaviour.
func TestMessageInjection_DisabledWhenNotConfigured(t *testing.T) {
	type EmptyArgs struct{}

	var gotInjector *toolautocall.MessageInjector

	rb := agenttest.NewResponseBuilder().
		AddFunctionCall("call1", "Check", `{}`).
		NewTurn().
		AddText("done")

	tools := []tool.Tool{
		functool.MustNew(functool.Config{Name: "Check", Description: "Checks injector"},
			func(ctx tool.Context, _ EmptyArgs) (string, error) {
				gotInjector = toolautocall.GetMessageInjector(ctx)
				return "ok", nil
			}),
	}

	runner := &agenttest.Runner{Responses: rb.Build()}

	var opts []agent.Option
	for _, tl := range tools {
		opts = append(opts, agent.WithTool(tl))
	}

	initialMessages := []*message.Message{message.NewText("start")}
	for _, err := range toolautocall.New(toolautocall.Config{
		EnableMessageInjection: false,
		NewID:                  func() string { return "" },
	}).Run(runner.Run, t.Context(), initialMessages, opts...) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if gotInjector != nil {
		t.Fatal("expected nil MessageInjector when EnableMessageInjection is false")
	}
}
