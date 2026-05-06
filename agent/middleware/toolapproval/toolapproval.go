// Copyright (c) Microsoft. All rights reserved.

// Package toolapproval provides middleware that manages human-in-the-loop
// tool approval with support for "don't ask again" standing rules.
//
// When an inner agent returns tool calls that require approval, this
// middleware surfaces approval requests one at a time to the caller. The
// caller may approve or deny individual calls, and optionally indicate that
// a tool (or a tool with specific arguments) should always be approved in
// the future for the lifetime of the session.
//
// Standing approval rules and queued requests are persisted across turns
// using the [agent.Session].
package toolapproval

import (
	"context"
	"encoding/json"
	"iter"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const stateKey = "toolApprovalState"

// Rule is a standing approval rule. If Arguments is empty, all invocations
// of the named tool are auto-approved. Otherwise only invocations with an
// exact argument match are auto-approved.
type Rule struct {
	ToolName  string `json:"toolName"`
	Arguments string `json:"arguments,omitempty"`
}

// matches reports whether r auto-approves a call to toolName with the given
// serialized arguments.
func (r Rule) matches(toolName, arguments string) bool {
	if r.ToolName != toolName {
		return false
	}
	if r.Arguments == "" {
		return true
	}
	return r.Arguments == arguments
}

// state is persisted in the session across turns.
type state struct {
	Rules              []Rule                                 `json:"rules,omitempty"`
	CollectedResponses []*message.ToolApprovalResponseContent `json:"collectedResponses,omitempty"`
	QueuedRequests     []*message.ToolApprovalRequestContent  `json:"queuedRequests,omitempty"`
}

func loadState(opts []agent.Option) state {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return state{}
	}
	var s state
	if found, _ := session.Get(stateKey, &s); found {
		return s
	}
	return state{}
}

func saveState(opts []agent.Option, s state) {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return
	}
	session.Set(stateKey, s)
}

// New creates a tool-approval middleware that wraps agent runs with
// human-in-the-loop approval management.
func New() agent.Middleware {
	return agent.MiddlewareFunc(run)
}

func run(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		st := loadState(opts)

		// Step 1: Process inbound approval responses from the caller.
		messages, st = prepareInbound(messages, st)

		// Step 2: If we have queued requests from a previous turn, drain any
		// that are now auto-approvable and surface the next one.
		drainAutoApprovable(&st)
		if len(st.QueuedRequests) > 0 {
			next := st.QueuedRequests[0]
			st.QueuedRequests = st.QueuedRequests[1:]
			saveState(opts, st)
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{next},
			}, nil)
			return
		}

		// Step 3: Main loop — call inner agent, classify approval requests.
		for {
			// Inject collected approval responses as user messages.
			callMessages := messages
			if len(st.CollectedResponses) > 0 {
				injected := responseMessage(st.CollectedResponses)
				callMessages = append(slices.Clone(messages), injected)
				st.CollectedResponses = nil
			}

			var updates []*agent.ResponseUpdate
			for update, err := range next(ctx, callMessages, opts...) {
				if err != nil {
					yield(nil, err)
					return
				}
				updates = append(updates, update)
			}

			// Collect all approval request contents from the response.
			var approvalRequests []*message.ToolApprovalRequestContent
			for _, u := range updates {
				for _, c := range u.Contents {
					if req, ok := c.(*message.ToolApprovalRequestContent); ok {
						approvalRequests = append(approvalRequests, req)
					}
				}
			}

			if len(approvalRequests) == 0 {
				// No approval requests — yield all updates and return.
				for _, u := range updates {
					if !yield(u, nil) {
						return
					}
				}
				saveState(opts, st)
				return
			}

			// Classify each approval request.
			var autoApproved []*message.ToolApprovalResponseContent
			var needsApproval []*message.ToolApprovalRequestContent
			for _, req := range approvalRequests {
				if matchesRule(st.Rules, req) {
					autoApproved = append(autoApproved, req.CreateResponse(true, ""))
				} else {
					needsApproval = append(needsApproval, req)
				}
			}

			if len(needsApproval) > 0 {
				// Surface the first unapproved request, queue the rest.
				first := needsApproval[0]
				st.QueuedRequests = append(st.QueuedRequests, needsApproval[1:]...)
				st.CollectedResponses = append(st.CollectedResponses, autoApproved...)

				// Yield non-approval updates, then the first approval request.
				for _, u := range updates {
					stripped := stripApprovalContents(u)
					if stripped != nil {
						if !yield(stripped, nil) {
							saveState(opts, st)
							return
						}
					}
				}
				if !yield(&agent.ResponseUpdate{
					Role:     message.RoleAssistant,
					Contents: []message.Content{first},
				}, nil) {
					saveState(opts, st)
					return
				}
				saveState(opts, st)
				return
			}

			// All were auto-approved — collect responses and loop to call
			// inner agent again with the approvals injected.
			st.CollectedResponses = append(st.CollectedResponses, autoApproved...)
			// Yield non-approval updates before looping.
			for _, u := range updates {
				stripped := stripApprovalContents(u)
				if stripped != nil {
					if !yield(stripped, nil) {
						saveState(opts, st)
						return
					}
				}
			}
		}
	}
}

// prepareInbound processes caller messages, extracting approval responses
// and any "always approve" flags into standing rules.
func prepareInbound(messages []*message.Message, st state) ([]*message.Message, state) {
	var cleaned []*message.Message
	for i, msg := range messages {
		var hasApproval bool
		for _, c := range msg.Contents {
			if resp, ok := c.(*message.ToolApprovalResponseContent); ok {
				hasApproval = true
				if fc, ok := resp.ToolCall.(*message.FunctionCallContent); ok && fc != nil {
					if resp.AlwaysApproveTool {
						st.Rules = append(st.Rules, Rule{ToolName: fc.Name})
					} else if resp.AlwaysApproveToolWithArgs {
						args, _ := json.Marshal(fc.Arguments)
						st.Rules = append(st.Rules, Rule{
							ToolName:  fc.Name,
							Arguments: string(args),
						})
					}
				}
				st.CollectedResponses = append(st.CollectedResponses, resp)
			}
		}
		if hasApproval {
			if cleaned == nil {
				cleaned = make([]*message.Message, 0, len(messages))
				cleaned = append(cleaned, messages[:i]...)
			}
			// Strip approval contents from the message, keep the rest.
			var remaining []message.Content
			for _, c := range msg.Contents {
				if _, ok := c.(*message.ToolApprovalResponseContent); !ok {
					remaining = append(remaining, c)
				}
			}
			if len(remaining) > 0 {
				clone := msg.Clone()
				clone.Contents = remaining
				cleaned = append(cleaned, clone)
			}
		} else if cleaned != nil {
			cleaned = append(cleaned, msg)
		}
	}
	if cleaned != nil {
		return cleaned, st
	}
	return messages, st
}

// drainAutoApprovable removes queued requests that now match a standing rule,
// adding auto-approve responses to collected.
func drainAutoApprovable(st *state) {
	if len(st.QueuedRequests) == 0 || len(st.Rules) == 0 {
		return
	}
	var remaining []*message.ToolApprovalRequestContent
	for _, req := range st.QueuedRequests {
		if matchesRule(st.Rules, req) {
			st.CollectedResponses = append(st.CollectedResponses, req.CreateResponse(true, ""))
		} else {
			remaining = append(remaining, req)
		}
	}
	st.QueuedRequests = remaining
}

func matchesRule(rules []Rule, req *message.ToolApprovalRequestContent) bool {
	fc, ok := req.ToolCall.(*message.FunctionCallContent)
	if !ok || fc == nil {
		return false
	}
	args, _ := json.Marshal(fc.Arguments)
	for _, r := range rules {
		if r.matches(fc.Name, string(args)) {
			return true
		}
	}
	return false
}

func responseMessage(responses []*message.ToolApprovalResponseContent) *message.Message {
	contents := make([]message.Content, len(responses))
	for i, r := range responses {
		contents[i] = r
	}
	return &message.Message{
		Role:     message.RoleUser,
		Contents: contents,
	}
}

func stripApprovalContents(u *agent.ResponseUpdate) *agent.ResponseUpdate {
	var stripped []message.Content
	for i, c := range u.Contents {
		if _, ok := c.(*message.ToolApprovalRequestContent); ok {
			if stripped == nil {
				stripped = make([]message.Content, 0, len(u.Contents))
				stripped = append(stripped, u.Contents[:i]...)
			}
		} else if stripped != nil {
			stripped = append(stripped, c)
		}
	}
	if stripped != nil {
		if len(stripped) == 0 {
			return nil
		}
		clone := *u
		clone.Contents = stripped
		return &clone
	}
	return u
}
