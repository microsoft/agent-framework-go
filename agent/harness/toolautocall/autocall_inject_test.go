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
// MessageInjectorFromContext, the injected message is sent to the provider on the next
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
				if mi := toolautocall.MessageInjectorFromContext(ctx); mi != nil {
					mi.EnqueueMessages(nil, message.New(&message.TextContent{Text: injectedText}))
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
	if slices.Contains(secondTurnMsgs, nil) {
		t.Fatal("expected nil injected message to be ignored")
	}
}

func TestMessageInjection_ApprovedApprovalResponseInjectionIsForwarded(t *testing.T) {
	type EmptyArgs struct{}

	const injectedText = "injected after approved tool"
	var downstreamMsgs []*message.Message

	tools := []tool.Tool{
		tool.ApprovalRequiredFunc(functool.MustNew(functool.Config{Name: "Injecting", Description: "Injects a message"},
			func(ctx tool.Context, _ EmptyArgs) (string, error) {
				if mi := toolautocall.MessageInjectorFromContext(ctx); mi != nil {
					mi.EnqueueMessages(message.New(&message.TextContent{Text: injectedText}))
				}
				return "approved tool done", nil
			})),
	}

	input := []*message.Message{
		message.NewText("start"),
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.ToolApprovalRequestContent{RequestID: "call1", ToolCall: &message.FunctionCallContent{CallID: "call1", Name: "Injecting", Arguments: `{}`}},
		}},
		message.New(&message.ToolApprovalResponseContent{RequestID: "call1", Approved: true, ToolCall: &message.FunctionCallContent{CallID: "call1", Name: "Injecting", Arguments: `{}`}}),
	}

	runner := &agenttest.Runner{Responses: agenttest.NewResponseBuilder(func(_ context.Context, msgs []*message.Message, _ ...agent.Option) {
		downstreamMsgs = slices.Clone(msgs)
	}).AddText("done").Build()}

	var opts []agent.Option
	for _, tl := range tools {
		opts = append(opts, agent.WithTool(tl))
	}

	for _, err := range toolautocall.New(toolautocall.Config{
		EnableMessageInjection: true,
		NewID:                  func() string { return "" },
	}).Run(runner.Run, t.Context(), input, opts...) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	foundInjected := slices.ContainsFunc(downstreamMsgs, func(m *message.Message) bool {
		return m != nil && slices.ContainsFunc(m.Contents, func(c message.Content) bool {
			tc, ok := c.(*message.TextContent)
			return ok && tc.Text == injectedText
		})
	})
	if !foundInjected {
		t.Fatalf("injected message %q not found in downstream provider call messages", injectedText)
	}
}

func TestMessageInjection_RespectsMaximumIterationsPerRequest(t *testing.T) {
	callCount := 0

	rb := agenttest.NewResponseBuilder(func(ctx context.Context, _ []*message.Message, _ ...agent.Option) {
		callCount++
		toolautocall.MessageInjectorFromContext(ctx).EnqueueMessages(message.NewText("first injected turn"))
	}).AddText("first response").
		NewTurn(func(ctx context.Context, _ []*message.Message, _ ...agent.Option) {
			callCount++
			toolautocall.MessageInjectorFromContext(ctx).EnqueueMessages(message.NewText("second injected turn"))
		}).AddText("second response").
		NewTurn(func(context.Context, []*message.Message, ...agent.Option) {
			callCount++
		}).AddText("third response")

	runner := &agenttest.Runner{Responses: rb.Build()}

	for _, err := range toolautocall.New(toolautocall.Config{
		EnableMessageInjection:      true,
		MaximumIterationsPerRequest: 1,
		NewID:                       func() string { return "" },
	}).Run(runner.Run, t.Context(), []*message.Message{message.NewText("start")}) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if callCount != 2 {
		t.Fatalf("expected 2 provider calls, got %d", callCount)
	}
}

// TestMessageInjection_DisabledWhenNotConfigured verifies that MessageInjectorFromContext
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
				gotInjector = toolautocall.MessageInjectorFromContext(ctx)
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
