// Copyright (c) Microsoft. All rights reserved.

package toolapproval_test

import (
	"context"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/middleware/toolapproval"
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

	mw := toolapproval.New()
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

	mw := toolapproval.New()
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
			NewTurn().
			AddText("done").
			Build(),
	}

	mw := toolapproval.New()
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

	mw := toolapproval.New()
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

func TestToolApproval_AlwaysApproveToolWithArgsResponse(t *testing.T) {
	req := &message.ToolApprovalRequestContent{
		RequestID: "r1",
		ToolCall:  &message.FunctionCallContent{CallID: "c1", Name: "deploy", Arguments: `{"env":"prod"}`},
	}

	resp := req.AlwaysApproveToolWithArgsResponse()
	if !resp.AlwaysApproveToolWithArgs {
		t.Error("expected AlwaysApproveToolWithArgs to be true")
	}
	if !resp.Approved {
		t.Error("expected Approved to be true")
	}
}
