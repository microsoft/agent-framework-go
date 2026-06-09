// Copyright (c) Microsoft. All rights reserved.

package toolapproval_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolapproval"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
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

// newNoopTool creates a named tool that does not require approval.
func newNoopTool(name string) tool.FuncTool {
	return functool.MustNew(functool.Config{Name: name},
		func(_ context.Context, _ struct{}) (string, error) { return "", nil })
}

// TestToolApproval_NonApprovalRequiredToolAutoApproved verifies that when both
// approval-required and non-approval-required tools appear as approval requests
// (as produced by toolautocall when any tool requires approval), the middleware
// surfaces only the approval-required request and auto-approves the rest.
func TestToolApproval_NonApprovalRequiredToolAutoApproved(t *testing.T) {
	deployFCC := &message.FunctionCallContent{CallID: "c-deploy", Name: "deploy"}
	listFCC := &message.FunctionCallContent{CallID: "c-list", Name: "list"}

	var innerCallMessages []*message.Message
	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			// Turn 1: return two approval requests — one for approval-required deploy,
			// one for non-approval-required list.
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "r-deploy", ToolCall: deployFCC},
					&message.ToolApprovalRequestContent{RequestID: "r-list", ToolCall: listFCC},
				},
			}).
			// Turn 2: inner agent is called with both auto-approved responses.
			NewTurn(func(_ context.Context, msgs []*message.Message, _ ...agent.Option) {
				innerCallMessages = msgs
			}).
			AddText("done").
			Build(),
	}

	mw := toolapproval.New(toolapproval.Config{})
	session := agenttest.CreateSession()
	opts := []agent.Option{
		agent.WithSession(session),
		agent.WithTool(tool.ApprovalRequiredFunc(newNoopTool("deploy"))),
		agent.WithTool(newNoopTool("list")), // does NOT require approval
	}

	// Turn 1: middleware should surface only deploy, auto-approve list.
	turn1 := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		opts...,
	)

	var approvalReqs []*message.ToolApprovalRequestContent
	for _, u := range turn1 {
		for _, c := range u.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				approvalReqs = append(approvalReqs, r)
			}
		}
	}
	if len(approvalReqs) != 1 {
		t.Fatalf("expected exactly 1 approval request (deploy), got %d", len(approvalReqs))
	}
	if approvalReqs[0].RequestID != "r-deploy" {
		t.Errorf("expected deploy approval request, got %q", approvalReqs[0].RequestID)
	}

	// Turn 2: approve deploy; inner agent should receive responses for both list and deploy.
	deployResp := approvalReqs[0].CreateResponse(true, "")
	turn2 := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{deployResp}}},
		opts...,
	)

	var gotDone bool
	for _, u := range turn2 {
		for _, c := range u.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "done" {
				gotDone = true
			}
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				t.Errorf("unexpected approval request in turn 2: %q", r.RequestID)
			}
		}
	}
	if !gotDone {
		t.Fatal("expected 'done' text after auto-approval of list tool")
	}

	// The inner agent must have received approval responses for both list and deploy.
	var sawListApproval, sawDeployApproval bool
	for _, msg := range innerCallMessages {
		for _, c := range msg.Contents {
			if resp, ok := c.(*message.ToolApprovalResponseContent); ok {
				switch resp.RequestID {
				case "r-list":
					sawListApproval = true
					if !resp.Approved {
						t.Error("expected list approval response to be approved=true")
					}
				case "r-deploy":
					sawDeployApproval = true
				}
			}
		}
	}
	if !sawListApproval {
		t.Error("inner agent did not receive auto-approval response for non-approval-required list tool")
	}
	if !sawDeployApproval {
		t.Error("inner agent did not receive approval response for deploy tool")
	}
}

// TestToolApproval_NonApprovalRequiredQueuedRequestDrained verifies that a
// queued approval request for a non-approval-required tool is drained
// automatically on the next turn (even though it was queued when the tool was
// not registered). This mirrors .NET's NonApprovalRequiredFunctionBypassingChatClient
// behaviour of re-injecting stored auto-approved calls without requiring human input.
func TestToolApproval_NonApprovalRequiredQueuedRequestDrained(t *testing.T) {
	deployFCC := &message.FunctionCallContent{CallID: "c-deploy", Name: "deploy"}
	listFCC := &message.FunctionCallContent{CallID: "c-list", Name: "list"}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			// Turn 1 (no tools in opts → both need approval): inner returns two requests.
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "r-deploy", ToolCall: deployFCC},
					&message.ToolApprovalRequestContent{RequestID: "r-list", ToolCall: listFCC},
				},
			}).
			// Turn 2 (list now registered as non-approval-required): drain list, get "done".
			NewTurn().
			AddText("done").
			Build(),
	}

	mw := toolapproval.New(toolapproval.Config{})
	session := agenttest.CreateSession()

	// Turn 1: no tool options → both deploy and list are treated as requiring approval.
	turn1 := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		agent.WithSession(session),
	)

	var deployReq *message.ToolApprovalRequestContent
	for _, u := range turn1 {
		for _, c := range u.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok && r.RequestID == "r-deploy" {
				deployReq = r
			}
		}
	}
	if deployReq == nil {
		t.Fatal("expected deploy approval request in turn 1")
	}

	// Turn 2: approve deploy; now register list as non-approval-required.
	// drainAutoApprovable should drain the queued list request automatically.
	deployResp := deployReq.CreateResponse(true, "")
	optsWithList := []agent.Option{
		agent.WithSession(session),
		agent.WithTool(newNoopTool("list")), // list does NOT require approval
	}
	turn2 := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{deployResp}}},
		optsWithList...,
	)

	var gotDone bool
	for _, u := range turn2 {
		for _, c := range u.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "done" {
				gotDone = true
			}
			if r, ok := c.(*message.ToolApprovalRequestContent); ok {
				t.Errorf("unexpected approval request in turn 2: %q", r.RequestID)
			}
		}
	}
	if !gotDone {
		t.Fatal("expected 'done' after queued non-approval-required request was drained")
	}
}

func TestToolApproval_AutoApprovalRule_ApprovesMatchingTool(t *testing.T) {
	fcc := &message.FunctionCallContent{CallID: "c1", Name: "ReadTool", Arguments: `{}`}

	// Turn 1: inner returns an approval request.
	// Turn 2 (triggered automatically after auto-approval): inner returns done.
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

	cfg := toolapproval.Config{
		AutoApprovalRules: []func(context.Context, *message.FunctionCallContent) (bool, error){
			func(_ context.Context, fc *message.FunctionCallContent) (bool, error) {
				return fc.Name == "ReadTool", nil
			},
		},
	}
	mw := toolapproval.New(cfg)
	session := agenttest.CreateSession()

	updates := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		agent.WithSession(session),
	)

	// Should receive "done" without any approval request surfaced to the caller.
	var gotDone bool
	for _, u := range updates {
		for _, c := range u.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "done" {
				gotDone = true
			}
			if _, ok := c.(*message.ToolApprovalRequestContent); ok {
				t.Fatal("expected no approval request to be surfaced when auto-approval rule matches")
			}
		}
	}
	if !gotDone {
		t.Error("expected 'done' text after auto-approval rule approved the tool")
	}
}

func TestToolApproval_AutoApprovalRule_DoesNotMatchSurfacesToCaller(t *testing.T) {
	fcc := &message.FunctionCallContent{CallID: "c1", Name: "DangerousTool", Arguments: `{}`}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "r1", ToolCall: fcc},
				},
			}).
			Build(),
	}

	cfg := toolapproval.Config{
		AutoApprovalRules: []func(context.Context, *message.FunctionCallContent) (bool, error){
			func(_ context.Context, fc *message.FunctionCallContent) (bool, error) {
				return fc.Name == "ReadTool", nil
			}, // only approves ReadTool
		},
	}
	mw := toolapproval.New(cfg)

	updates := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
	)

	var approvalReqs []*message.ToolApprovalRequestContent
	for _, u := range updates {
		for _, c := range u.Contents {
			if req, ok := c.(*message.ToolApprovalRequestContent); ok {
				approvalReqs = append(approvalReqs, req)
			}
		}
	}
	if len(approvalReqs) != 1 {
		t.Fatalf("expected 1 approval request surfaced, got %d", len(approvalReqs))
	}
	fc, ok := approvalReqs[0].ToolCall.(*message.FunctionCallContent)
	if !ok || fc.Name != "DangerousTool" {
		t.Errorf("expected DangerousTool to be surfaced, got %v", approvalReqs[0].ToolCall)
	}
}

func TestToolApproval_MultipleAutoApprovalRules_FirstMatchWins(t *testing.T) {
	fcc := &message.FunctionCallContent{CallID: "c1", Name: "SpecialTool", Arguments: `{}`}

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

	rule1Called := false
	rule2Called := false
	cfg := toolapproval.Config{
		AutoApprovalRules: []func(context.Context, *message.FunctionCallContent) (bool, error){
			func(_ context.Context, fc *message.FunctionCallContent) (bool, error) {
				rule1Called = true
				return fc.Name == "SpecialTool", nil
			},
			func(_ context.Context, _ *message.FunctionCallContent) (bool, error) {
				rule2Called = true
				return true, nil // should not be reached
			},
		},
	}
	mw := toolapproval.New(cfg)
	session := agenttest.CreateSession()

	collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		agent.WithSession(session),
	)

	if !rule1Called {
		t.Error("expected first auto-approval rule to be called")
	}
	if rule2Called {
		t.Error("expected second auto-approval rule to NOT be called when first already matched")
	}
}

func TestToolApproval_StandingRuleTakesPrecedenceOverAutoApprovalRule(t *testing.T) {
	fcc := &message.FunctionCallContent{CallID: "c1", Name: "MyTool", Arguments: `{}`}

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

	heuristicCalled := false
	cfg := toolapproval.Config{
		AutoApprovalRules: []func(context.Context, *message.FunctionCallContent) (bool, error){
			func(_ context.Context, _ *message.FunctionCallContent) (bool, error) {
				heuristicCalled = true
				return true, nil
			},
		},
	}
	mw := toolapproval.New(cfg)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	// Turn 1: auto-approval rule is called (no standing rule yet).
	updates := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		opts...,
	)

	if !heuristicCalled {
		t.Error("expected auto-approval rule to be called on first turn")
	}

	var gotDone bool
	for _, u := range updates {
		for _, c := range u.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "done" {
				gotDone = true
			}
		}
	}
	if !gotDone {
		t.Error("expected 'done' after auto-approval rule approved on first turn")
	}
}

func TestToolApproval_AutoApprovalRule_ApprovesQueuedRequests(t *testing.T) {
	fcc1 := &message.FunctionCallContent{CallID: "c1", Name: "SafeTool", Arguments: `{}`}
	fcc2 := &message.FunctionCallContent{CallID: "c2", Name: "DangerousTool", Arguments: `{}`}

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

	// AutoApprovalRule approves SafeTool but not DangerousTool.
	cfg := toolapproval.Config{
		AutoApprovalRules: []func(context.Context, *message.FunctionCallContent) (bool, error){
			func(_ context.Context, fc *message.FunctionCallContent) (bool, error) {
				return fc.Name == "SafeTool", nil
			},
		},
	}
	mw := toolapproval.New(cfg)
	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	// Turn 1: r1 (SafeTool) is auto-approved; r2 (DangerousTool) is surfaced.
	updates := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		opts...,
	)

	var surfacedReqs []*message.ToolApprovalRequestContent
	for _, u := range updates {
		for _, c := range u.Contents {
			if req, ok := c.(*message.ToolApprovalRequestContent); ok {
				surfacedReqs = append(surfacedReqs, req)
			}
		}
	}
	if len(surfacedReqs) != 1 {
		t.Fatalf("expected 1 surfaced request (DangerousTool), got %d", len(surfacedReqs))
	}
	fc, ok := surfacedReqs[0].ToolCall.(*message.FunctionCallContent)
	if !ok || fc.Name != "DangerousTool" {
		t.Errorf("expected DangerousTool to be surfaced, got %v", surfacedReqs[0].ToolCall)
	}
}

func TestToolApproval_AutoApprovalRule_ErrorFailsRun(t *testing.T) {
	ruleErr := errors.New("auto-approval rule failed")
	fcc := &message.FunctionCallContent{CallID: "c1", Name: "ReadTool", Arguments: `{}`}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "r1", ToolCall: fcc},
				},
			}).
			Build(),
	}

	mw := toolapproval.New(toolapproval.Config{
		AutoApprovalRules: []func(context.Context, *message.FunctionCallContent) (bool, error){
			func(_ context.Context, _ *message.FunctionCallContent) (bool, error) { return false, ruleErr },
		},
	})

	var gotErr error
	for _, err := range mw.Run(runner.Run, context.Background(), []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}},
	}) {
		if err != nil {
			gotErr = err
			break
		}
	}
	if !errors.Is(gotErr, ruleErr) {
		t.Fatalf("expected rule error %v, got %v", ruleErr, gotErr)
	}
}

func TestToolApproval_QueuedStandingRuleShortCircuitsAutoApprovalEvaluation(t *testing.T) {
	tool1 := &message.FunctionCallContent{CallID: "c1", Name: "MyTool", Arguments: `{}`}
	tool2 := &message.FunctionCallContent{CallID: "c2", Name: "MyTool", Arguments: `{}`}

	runner := &agenttest.Runner{
		Responses: agenttest.NewResponseBuilder().
			Add(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: []message.Content{
					&message.ToolApprovalRequestContent{RequestID: "r1", ToolCall: tool1},
					&message.ToolApprovalRequestContent{RequestID: "r2", ToolCall: tool2},
				},
			}).
			NewTurn().
			AddText("done").
			Build(),
	}

	ruleErr := errors.New("auto-approval rule should be skipped")
	ruleCalls := 0
	mw := toolapproval.New(toolapproval.Config{
		AutoApprovalRules: []func(context.Context, *message.FunctionCallContent) (bool, error){
			func(_ context.Context, _ *message.FunctionCallContent) (bool, error) {
				ruleCalls++
				if ruleCalls <= 2 {
					return false, nil
				}
				return false, ruleErr
			},
		},
	})

	session := agenttest.CreateSession()
	opts := []agent.Option{agent.WithSession(session)}

	turn1 := collectUpdates(t, mw, runner.Run,
		[]*message.Message{{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "go"}}}},
		opts...,
	)

	var req *message.ToolApprovalRequestContent
	for _, u := range turn1 {
		for _, c := range u.Contents {
			if r, ok := c.(*message.ToolApprovalRequestContent); ok && r.RequestID == "r1" {
				req = r
			}
		}
	}
	if req == nil {
		t.Fatal("expected first approval request to be surfaced")
	}

	var turn2 []*agent.ResponseUpdate
	for u, err := range mw.Run(runner.Run, context.Background(), []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{req.AlwaysApproveToolResponse()}},
	}, opts...) {
		if err != nil {
			t.Fatalf("expected queued standing-rule match to short-circuit auto-approval rule errors, got %v", err)
		}
		turn2 = append(turn2, u)
	}

	var gotDone bool
	for _, u := range turn2 {
		for _, c := range u.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "done" {
				gotDone = true
			}
		}
	}
	if !gotDone {
		t.Fatal("expected done after queued request was auto-approved by standing rule")
	}
	if ruleCalls != 2 {
		t.Fatalf("expected auto-approval rule to be called only in turn 1, got %d calls", ruleCalls)
	}
}
