// Copyright (c) Microsoft. All rights reserved.

package toolapproval_test

import (
	"context"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
)

func collectUpdates(t *testing.T, mw agent.Middleware, next agent.RunFunc, messages []*message.Message, opts ...agent.Option) []*agent.ResponseUpdate {
	t.Helper()
	var updates []*agent.ResponseUpdate
	for u, err := range mw.Run(next, context.Background(), messages, opts...) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updates = append(updates, u)
	}
	return updates
}

func TestToolApproval_PassthroughWithoutApprovalRequests(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().AddText("hello").Build(),
	}

	mw := toolapproval.New(toolapproval.Config{})
	updates := collectUpdates(t, mw, runner.Run, []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	})

	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if tc, ok := updates[0].Contents[0].(*message.TextContent); !ok || tc.Text != "hello" {
		t.Errorf("expected text 'hello', got %v", updates[0].Contents)
	}
}

func TestToolApproval_StreamsNonApprovalBeforeInnerCompletes(t *testing.T) {
	innerCompleted := false
	next := func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			if !yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "progress"}},
			}, nil) {
				return
			}
			if !yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.TextContent{Text: "needs approval"},
					&message.ToolApprovalRequestContent{
						RequestID: "r1",
						ToolCall: &message.FunctionCallContent{
							CallID:    "c1",
							Name:      "deploy",
							Arguments: `{"env":"prod"}`,
						},
					},
				},
			}, nil) {
				return
			}
			innerCompleted = true
		}
	}

	mw := toolapproval.New(toolapproval.Config{})
	var updates []*agent.ResponseUpdate
	for u, err := range mw.Run(next, context.Background(), []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}},
	}) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		updates = append(updates, u)
		if len(updates) == 1 && innerCompleted {
			t.Fatal("expected first streamed non-approval update before inner stream completes")
		}
	}

	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}
	if tc, ok := updates[0].Contents[0].(*message.TextContent); !ok || tc.Text != "progress" {
		t.Fatalf("expected first update to be progress text, got %#v", updates[0].Contents)
	}
	if tc, ok := updates[1].Contents[0].(*message.TextContent); !ok || tc.Text != "needs approval" {
		t.Fatalf("expected second update to be stripped text, got %#v", updates[1].Contents)
	}
	if _, ok := updates[2].Contents[0].(*message.ToolApprovalRequestContent); !ok {
		t.Fatalf("expected final update to surface approval request, got %#v", updates[2].Contents)
	}
}

func TestToolApproval_SurfacesFirstApprovalRequest(t *testing.T) {
	fcc1 := &message.FunctionCallContent{CallID: "c1", Name: "deploy", Arguments: `{"env":"prod"}`}
	fcc2 := &message.FunctionCallContent{CallID: "c2", Name: "restart", Arguments: `{}`}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "r1", ToolCall: fcc1},
					&message.ToolApprovalRequestContent{RequestID: "r2", ToolCall: fcc2},
				},
			}).
			Build(),
	}

	mw := toolapproval.New(toolapproval.Config{})
	session := agenttest.CreateSession()
	updates := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		agent.WithSession(session),
	)

	// Should receive exactly one approval request (the first one).
	var approvalReqs []*message.ToolApprovalRequestContent
	for _, u := range updates {
		for _, c := range u.Contents {
			if req, ok := c.(*message.ToolApprovalRequestContent); ok {
				approvalReqs = append(approvalReqs, req)
			}
		}
	}
	if len(approvalReqs) != 1 {
		t.Fatalf("expected 1 approval request, got %d", len(approvalReqs))
	}
	if approvalReqs[0].RequestID != "r1" {
		t.Errorf("expected request ID 'r1', got %q", approvalReqs[0].RequestID)
	}
}

func TestToolApproval_AlwaysApproveToolCreatesRule(t *testing.T) {
	fcc := &message.FunctionCallContent{CallID: "c1", Name: "deploy", Arguments: `{"env":"prod"}`}

	// Turn 1: inner agent returns an approval request.
	// Turn 2: inner agent returns the same tool approval request again — should be auto-approved.
	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "r1", ToolCall: fcc},
				},
			}).
			NewTurn(func(_ context.Context, messages []*message.Message, _ ...agent.Option) {
				var sawResponse bool
				for _, msg := range messages {
					for _, c := range msg.Contents {
						if _, ok := c.(*message.AlwaysApproveToolApprovalResponseContent); ok {
							t.Fatal("always-approve wrapper should be unwrapped before forwarding to inner agent")
						}
						if _, ok := c.(*message.ToolApprovalResponseContent); ok {
							sawResponse = true
						}
					}
				}
				if !sawResponse {
					t.Fatal("expected approval response to be forwarded to inner agent")
				}
			}).
			AddText("done").
			Build(),
	}

	mw := toolapproval.New(toolapproval.Config{})
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	// Turn 1: Get the approval request.
	updates := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		opts...,
	)
	var req *message.ToolApprovalRequestContent
	for _, u := range updates {
		for _, c := range u.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				req = r
			}
		}
	}
	if req == nil {
		t.Fatal("expected approval request in turn 1")
	}

	// Respond with "always approve this tool".
	alwaysApproveResp := req.AlwaysApproveToolResponse()

	// Turn 2: Send the always-approve response. The queued requests from the
	// inner agent should now be auto-approved and the inner agent called again.
	updates = collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{alwaysApproveResp}}},
		opts...,
	)

	// Should get the "done" text from the inner agent (approval was auto-applied).
	var gotDone bool
	for _, u := range updates {
		for _, c := range u.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "done" {
				gotDone = true
			}
		}
	}
	if !gotDone {
		t.Error("expected 'done' text after auto-approval, but did not get it")
	}
}

func TestToolApproval_QueuedRequestsSurfacedOneAtATime(t *testing.T) {
	fcc1 := &message.FunctionCallContent{CallID: "c1", Name: "deploy", Arguments: `{"env":"prod"}`}
	fcc2 := &message.FunctionCallContent{CallID: "c2", Name: "restart", Arguments: `{}`}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "r1", ToolCall: fcc1},
					&message.ToolApprovalRequestContent{RequestID: "r2", ToolCall: fcc2},
				},
			}).
			Build(),
	}

	mw := toolapproval.New(toolapproval.Config{})
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	// Turn 1: Get first approval request.
	updates := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		opts...,
	)
	var firstReq *message.ToolApprovalRequestContent
	for _, u := range updates {
		for _, c := range u.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				firstReq = r
			}
		}
	}
	if firstReq == nil || firstReq.RequestID != "r1" {
		t.Fatal("expected first approval request to be r1")
	}

	// Turn 2: Approve r1, should get r2 next.
	resp := firstReq.CreateResponse(true, "")
	updates = collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{resp}}},
		opts...,
	)

	var secondReq *message.ToolApprovalRequestContent
	for _, u := range updates {
		for _, c := range u.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				secondReq = r
			}
		}
	}
	if secondReq == nil || secondReq.RequestID != "r2" {
		t.Fatalf("expected second approval request to be r2, got %v", secondReq)
	}
}

func TestToolApproval_AlwaysApproveToolWithArgumentsResponse(t *testing.T) {
	req := &message.ToolApprovalRequestContent{
		RequestID: "r1",
		ToolCall:  &message.FunctionCallContent{CallID: "c1", Name: "deploy", Arguments: `{"env":"prod"}`},
	}

	resp := req.AlwaysApproveToolWithArgumentsResponse()
	if !resp.AlwaysApproveToolWithArguments {
		t.Error("expected AlwaysApproveToolWithArguments to be true")
	}
	if resp.InnerResponse == nil || !resp.InnerResponse.Approved {
		t.Error("expected Approved to be true")
	}
}

func TestToolApproval_AlwaysApproveToolWithArgumentsMatchesByValue(t *testing.T) {
	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{
						RequestID: "r1",
						ToolCall: &message.FunctionCallContent{
							CallID:    "c1",
							Name:      "deploy",
							Arguments: `{"a":1,"b":2}`,
						},
					},
				},
			}).
			NewTurn().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{
						RequestID: "r2",
						ToolCall: &message.FunctionCallContent{
							CallID:    "c2",
							Name:      "deploy",
							Arguments: `{"b":2,"a":1}`,
						},
					},
				},
			}).
			NewTurn().
			AddText("done").
			Build(),
	}

	mw := toolapproval.New(toolapproval.Config{})
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	updates := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		opts...,
	)

	var req *message.ToolApprovalRequestContent
	for _, u := range updates {
		for _, c := range u.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				req = r
			}
		}
	}
	if req == nil {
		t.Fatal("expected approval request in turn 1")
	}

	updates = collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{req.AlwaysApproveToolWithArgumentsResponse()}}},
		opts...,
	)

	var sawDone bool
	for _, u := range updates {
		for _, c := range u.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "done" {
				sawDone = true
			}
			if _, ok := c.(*message.ToolApprovalRequestContent); ok {
				t.Fatal("expected second request to be auto-approved after argument-value match")
			}
		}
	}
	if !sawDone {
		t.Fatal("expected done after auto-approval with argument-value match")
	}
}
