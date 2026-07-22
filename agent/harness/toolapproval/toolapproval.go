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
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

const stateKey = "toolApprovalState"

// Rule is a standing approval rule. If Arguments is nil, all invocations of
// the named tool are auto-approved. Otherwise only invocations with an exact
// argument map match are auto-approved. A non-nil empty Arguments map matches
// only calls with no arguments.
type Rule struct {
	ToolName  string            `json:"toolName"`
	Arguments map[string]string `json:"arguments"`
}

// matches reports whether r auto-approves a call to toolName with the given
// serialized arguments.
func (r Rule) matches(toolName string, arguments map[string]string) bool {
	if r.ToolName != toolName {
		return false
	}
	if r.Arguments == nil {
		return true
	}
	return argumentMapsEqual(r.Arguments, arguments)
}

// state is persisted in the session across turns.
type state struct {
	Rules                      []Rule                                         `json:"rules,omitempty"`
	CollectedApprovalResponses []*message.ToolApprovalResponseContent         `json:"collectedResponses,omitempty"`
	QueuedApprovalRequests     []*message.ToolApprovalRequestContent          `json:"queuedRequests,omitempty"`
	SurfacedApprovalRequests   map[string]*message.ToolApprovalRequestContent `json:"surfacedRequests,omitempty"`
}

func loadState(opts []agent.Option) state {
	session, ok := agent.GetOption(opts, agent.WithSession)
	if !ok {
		return normalizedState(state{})
	}
	var s state
	if found, _ := session.Get(stateKey, &s); found {
		return normalizedState(s)
	}
	return normalizedState(state{})
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
func New(cfg Config) agent.Middleware {
	return agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return run(cfg, next, ctx, messages, opts...)
	})
}

// Config configures tool-approval middleware behavior.
type Config struct {
	// AutoApprovalRules is an optional list of heuristic functions evaluated after
	// standing rules (derived from prior user approvals) but before surfacing the
	// approval request to the caller. Each rule receives the tool call and returns
	// (approved, error). Returning approved=true auto-approves the request. Rules
	// are evaluated in order; the first returning approved=true causes the request
	// to be auto-approved without prompting the caller. Returning an error fails
	// the current run.
	AutoApprovalRules []func(context.Context, *message.FunctionCallContent) (bool, error)

	// DisableApprovalResponseBinding disables rebinding inbound approval responses
	// to the tool approval requests previously surfaced by this middleware.
	//
	// When false (the default), only approval responses tied to a surfaced or
	// history-carried approval request are honored, and the recorded request's tool
	// call is injected downstream so an approved call matches what was surfaced for
	// approval. When true, inbound approval responses are forwarded unchanged.
	DisableApprovalResponseBinding bool
}

func run(cfg Config, next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		st := loadState(opts)

		// Step 1: Process inbound approval responses from the caller.
		messages, st = prepareInbound(messages, st, !cfg.DisableApprovalResponseBinding)

		// Step 2: If we have queued requests from a previous turn, drain any
		// that are now auto-approvable and surface the next one.
		if err := drainAutoApprovable(ctx, cfg, &st, opts); err != nil {
			yield(nil, err)
			return
		}
		if len(st.QueuedApprovalRequests) > 0 {
			next := st.QueuedApprovalRequests[0]
			st.QueuedApprovalRequests = st.QueuedApprovalRequests[1:]
			if !cfg.DisableApprovalResponseBinding {
				recordSurfacedApprovalRequests(&st, next)
			}
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
			if len(st.CollectedApprovalResponses) > 0 {
				injected := responseMessage(st.CollectedApprovalResponses)
				callMessages = append(slices.Clone(messages), injected)
				st.CollectedApprovalResponses = nil
			}

			var approvalRequests []*message.ToolApprovalRequestContent
			for update, err := range next(ctx, callMessages, opts...) {
				if err != nil {
					yield(nil, err)
					return
				}
				if update == nil {
					if !yield(nil, nil) {
						saveState(opts, st)
						return
					}
					continue
				}
				stripped, requests := splitApprovalRequestContents(update)
				approvalRequests = append(approvalRequests, requests...)
				if stripped != nil {
					if !yield(stripped, nil) {
						saveState(opts, st)
						return
					}
				}
			}

			if len(approvalRequests) == 0 {
				// No approval requests — all streaming updates were already yielded.
				saveState(opts, st)
				return
			}

			// Classify each approval request.
			var autoApproved []*message.ToolApprovalResponseContent
			var needsApproval []*message.ToolApprovalRequestContent
			for _, req := range approvalRequests {
				approved, err := isAutoApprovable(ctx, cfg, st.Rules, opts, req)
				if err != nil {
					yield(nil, err)
					return
				}
				if approved {
					autoApproved = append(autoApproved, req.CreateResponse(true, ""))
				} else {
					needsApproval = append(needsApproval, req)
				}
			}

			if len(needsApproval) > 0 {
				// Surface the first unapproved request, queue the rest.
				first := needsApproval[0]
				st.QueuedApprovalRequests = append(st.QueuedApprovalRequests, needsApproval[1:]...)
				st.CollectedApprovalResponses = append(st.CollectedApprovalResponses, autoApproved...)
				if !cfg.DisableApprovalResponseBinding {
					recordSurfacedApprovalRequests(&st, first)
				}

				// Non-approval updates were already yielded during streaming.
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
			st.CollectedApprovalResponses = append(st.CollectedApprovalResponses, autoApproved...)
			// Non-approval updates were already yielded during streaming.
		}
	}
}

// prepareInbound processes caller messages, extracting approval responses
// and any "always approve" flags into standing rules.
func prepareInbound(messages []*message.Message, st state, bindApprovalResponses bool) ([]*message.Message, state) {
	knownRequests := make(map[string]*message.ToolApprovalRequestContent)
	if bindApprovalResponses {
		knownRequests = knownApprovalRequests(messages, st)
	}

	var cleaned []*message.Message
	for i, msg := range messages {
		var hasApproval bool
		var remaining []message.Content
		for _, c := range msg.Contents {
			switch resp := c.(type) {
			case *message.AlwaysApproveToolApprovalResponseContent:
				hasApproval = true
				bound := bindApprovalResponse(resp.InnerResponse, &st, knownRequests, bindApprovalResponses)
				addApprovalRuleFromResponse(&st, resp, bound)
			case *message.ToolApprovalResponseContent:
				hasApproval = true
				bindApprovalResponse(resp, &st, knownRequests, bindApprovalResponses)
			default:
				if c != nil {
					remaining = append(remaining, c)
				}
			}
		}
		if hasApproval {
			if cleaned == nil {
				cleaned = make([]*message.Message, 0, len(messages))
				cleaned = append(cleaned, messages[:i]...)
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

func normalizedState(s state) state {
	if s.SurfacedApprovalRequests == nil {
		s.SurfacedApprovalRequests = make(map[string]*message.ToolApprovalRequestContent)
	}
	return s
}

func knownApprovalRequests(messages []*message.Message, st state) map[string]*message.ToolApprovalRequestContent {
	known := make(map[string]*message.ToolApprovalRequestContent, len(st.SurfacedApprovalRequests))
	for requestID, req := range st.SurfacedApprovalRequests {
		if req != nil {
			known[requestID] = req
		}
	}
	for _, msg := range messages {
		for _, c := range msg.Contents {
			req, ok := c.(*message.ToolApprovalRequestContent)
			if !ok || req == nil || req.RequestID == "" {
				continue
			}
			known[req.RequestID] = req
		}
	}
	return known
}

func bindApprovalResponse(resp *message.ToolApprovalResponseContent, st *state, knownRequests map[string]*message.ToolApprovalRequestContent, bind bool) *message.ToolApprovalResponseContent {
	if resp == nil {
		return nil
	}
	if !bind {
		st.CollectedApprovalResponses = append(st.CollectedApprovalResponses, resp)
		return resp
	}

	matchedRequest, ok := knownRequests[resp.RequestID]
	if !ok || matchedRequest == nil {
		return nil
	}

	delete(knownRequests, resp.RequestID)
	delete(st.SurfacedApprovalRequests, resp.RequestID)

	bound := &message.ToolApprovalResponseContent{
		ContentHeader: cloneContentHeader(resp.ContentHeader),
		RequestID:     resp.RequestID,
		Reason:        resp.Reason,
		Approved:      resp.Approved,
		ToolCall:      cloneToolCallContent(matchedRequest.ToolCall),
	}
	st.CollectedApprovalResponses = append(st.CollectedApprovalResponses, bound)
	return bound
}

func addApprovalRuleFromResponse(st *state, resp *message.AlwaysApproveToolApprovalResponseContent, bound *message.ToolApprovalResponseContent) {
	if resp == nil || bound == nil {
		return
	}
	fc, ok := bound.ToolCall.(*message.FunctionCallContent)
	if !ok || fc == nil {
		return
	}
	if resp.AlwaysApproveTool {
		addRuleIfNotExists(st, Rule{ToolName: fc.Name})
		return
	}
	if !resp.AlwaysApproveToolWithArguments {
		return
	}
	args, err := serializeArguments(fc.Arguments)
	if err != nil {
		return
	}
	addRuleIfNotExists(st, Rule{
		ToolName:  fc.Name,
		Arguments: args,
	})
}

func recordSurfacedApprovalRequests(st *state, requests ...*message.ToolApprovalRequestContent) {
	for _, req := range requests {
		if req == nil || req.RequestID == "" {
			continue
		}
		st.SurfacedApprovalRequests[req.RequestID] = snapshotToolApprovalRequest(req)
	}
}

// drainAutoApprovable removes queued requests that now match a standing rule,
// are for tools that do not require approval, or match an auto-approval rule,
// adding auto-approve responses to collected.
func drainAutoApprovable(ctx context.Context, cfg Config, st *state, opts []agent.Option) error {
	if len(st.QueuedApprovalRequests) == 0 {
		return nil
	}
	var remaining []*message.ToolApprovalRequestContent
	for _, req := range st.QueuedApprovalRequests {
		approved, err := isAutoApprovable(ctx, cfg, st.Rules, opts, req)
		if err != nil {
			return err
		}
		if approved {
			st.CollectedApprovalResponses = append(st.CollectedApprovalResponses, req.CreateResponse(true, ""))
		} else {
			remaining = append(remaining, req)
		}
	}
	st.QueuedApprovalRequests = remaining
	return nil
}

func matchesRule(rules []Rule, req *message.ToolApprovalRequestContent) bool {
	fc, ok := req.ToolCall.(*message.FunctionCallContent)
	if !ok || fc == nil {
		return false
	}
	args, err := serializeArguments(fc.Arguments)
	if err != nil {
		return false
	}
	for _, r := range rules {
		if r.matches(fc.Name, args) {
			return true
		}
	}
	return false
}

// isNotApprovalRequired reports whether the tool referenced by req does not
// require user approval, based on the tools in opts. It returns false
// (conservatively requiring approval) when the tool cannot be identified.
func isNotApprovalRequired(req *message.ToolApprovalRequestContent, opts []agent.Option) bool {
	fc, ok := req.ToolCall.(*message.FunctionCallContent)
	if !ok || fc == nil {
		return false
	}
	for t := range agent.AllOptions(opts, agent.WithTool) {
		st, ok := t.(tool.SchemaTool)
		if !ok || st.Name() != fc.Name {
			continue
		}
		if at, ok := t.(tool.ApprovalRequiredTool); ok {
			return !at.ApprovalRequired()
		}
		// Tool found but not an ApprovalRequiredTool — does not require approval.
		return true
	}
	return false
}

// isAutoApprovable reports whether a tool approval request should be automatically approved
// without surfacing it to the caller.
//
// Standing rules and tools not requiring approval are checked first (cheaply), before evaluating
// configured auto-approval rules. This matches the .NET MatchesRule || MatchesAutoApprovalRule
// evaluation pattern used in ToolApprovalAgent.
func isAutoApprovable(ctx context.Context, cfg Config, rules []Rule, opts []agent.Option, req *message.ToolApprovalRequestContent) (bool, error) {
	if matchesRule(rules, req) || isNotApprovalRequired(req, opts) {
		return true, nil
	}
	return matchesAutoApprovalRules(ctx, cfg.AutoApprovalRules, req)
}

// matchesAutoApprovalRules returns true if any configured auto-approval rule
// approves the request. Rules are evaluated in order; the first returning true
// wins. Returns false when rules is empty or the request is not a function call.
func matchesAutoApprovalRules(ctx context.Context, rules []func(context.Context, *message.FunctionCallContent) (bool, error), req *message.ToolApprovalRequestContent) (bool, error) {
	if len(rules) == 0 {
		return false, nil
	}
	fc, ok := req.ToolCall.(*message.FunctionCallContent)
	if !ok || fc == nil {
		return false, nil
	}
	for _, rule := range rules {
		if rule == nil {
			continue
		}
		matches, err := rule(ctx, fc)
		if err != nil {
			return false, err
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

func serializeArguments(arguments string) (map[string]string, error) {
	if strings.TrimSpace(arguments) == "" {
		return map[string]string{}, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &values); err != nil {
		return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
	}
	if len(values) == 0 {
		return map[string]string{}, nil
	}
	serialized := make(map[string]string, len(values))
	for k, v := range values {
		serialized[k] = string(v)
	}
	return serialized, nil
}

func argumentMapsEqual(a, b map[string]string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || av != bv {
			return false
		}
	}
	return true
}

func addRuleIfNotExists(st *state, rule Rule) {
	for _, existing := range st.Rules {
		if existing.ToolName == rule.ToolName && argumentMapsEqual(existing.Arguments, rule.Arguments) {
			return
		}
	}
	st.Rules = append(st.Rules, rule)
}

func snapshotToolApprovalRequest(req *message.ToolApprovalRequestContent) *message.ToolApprovalRequestContent {
	if req == nil {
		return nil
	}
	return &message.ToolApprovalRequestContent{
		ContentHeader: cloneContentHeader(req.ContentHeader),
		RequestID:     req.RequestID,
		ToolCall:      cloneToolCallContent(req.ToolCall),
	}
}

func cloneToolCallContent(content message.ToolCallContent) message.ToolCallContent {
	switch content := content.(type) {
	case nil:
		return nil
	case *message.FunctionCallContent:
		if content == nil {
			return nil
		}
		cloned := *content
		cloned.ContentHeader = cloneContentHeader(content.ContentHeader)
		return &cloned
	case *message.MCPServerToolCallContent:
		if content == nil {
			return nil
		}
		cloned := *content
		cloned.ContentHeader = cloneContentHeader(content.ContentHeader)
		return &cloned
	default:
		return content
	}
}

func cloneContentHeader(header message.ContentHeader) message.ContentHeader {
	return message.ContentHeader{
		AdditionalProperties: maps.Clone(header.AdditionalProperties),
		Annotations:          slices.Clone(header.Annotations),
		RawRepresentation:    header.RawRepresentation,
	}
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

func splitApprovalRequestContents(u *agent.ResponseUpdate) (*agent.ResponseUpdate, []*message.ToolApprovalRequestContent) {
	var stripped []message.Content
	var requests []*message.ToolApprovalRequestContent
	for i, c := range u.Contents {
		if req, ok := c.(*message.ToolApprovalRequestContent); ok {
			if stripped == nil {
				stripped = make([]message.Content, 0, len(u.Contents))
				stripped = append(stripped, u.Contents[:i]...)
			}
			requests = append(requests, req)
		} else if stripped != nil {
			stripped = append(stripped, c)
		}
	}
	if stripped != nil {
		if len(stripped) == 0 {
			return nil, requests
		}
		clone := *u
		clone.Contents = stripped
		return &clone, requests
	}
	return u, nil
}
