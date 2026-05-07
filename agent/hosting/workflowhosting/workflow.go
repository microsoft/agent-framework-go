// Copyright (c) Microsoft. All rights reserved.

// Package workflowhosting hosts an [agent.Agent] as a workflow
// [workflow.Executor], so the agent can participate in graphs alongside
// regular executors and other hosted agents.
package workflowhosting

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	agentSessionStateKey     = "agent_session"
	agentTurnEmitStateKey    = "agent_turn_emit"
	agentBufferedStateKey    = "agent_buffered"
	pendingApprovalsStateKey = "agent_pending_approvals"
	pendingCallsStateKey     = "agent_pending_calls"
)

// Config configures how an [agent.Agent] is hosted as a workflow
// [workflow.Executor].
type Config struct {
	// EmitUpdateEvents controls whether streaming [agent.ResponseUpdate] outputs
	// are emitted as the agent runs. A [workflow.TurnToken] with
	// [workflow.TurnToken.EmitEvents] set overrides this default for that turn.
	EmitUpdateEvents bool

	// EmitResponseEvents controls whether an aggregated [agent.Response] output is
	// emitted at the end of each turn.
	EmitResponseEvents bool

	// DisableMessageForwarding disables forwarding of incoming messages
	// downstream before the agent runs. By default (zero value), incoming
	// messages are forwarded so downstream nodes observe the full
	// conversation. Set to true for strict pipelines where each node should
	// only forward its own output.
	DisableMessageForwarding bool

	// DisableRoleReassignment disables rewriting incoming
	// [message.RoleAssistant] messages whose [message.Message.AuthorName]
	// does not match this agent to [message.RoleUser]. By default (zero
	// value), such messages are reassigned so the conversation between
	// agents appears, to each agent, as messages from "the user". Set to
	// true to preserve original roles.
	DisableRoleReassignment bool

	// InterceptUserInputRequests controls how [message.ToolApprovalRequestContent]
	// produced by the agent is dispatched.
	//
	// When false (the default), each request is raised as a workflow
	// [workflow.ExternalRequest] via [workflow.Context.PostRequest], and
	// the matching [workflow.ExternalResponse] is delivered back to this
	// executor by the runner.
	//
	// When true, each request is sent as a regular workflow message (via
	// [workflow.Context.SendMessage]) so other executors in the graph can
	// handle the approval; the matching
	// [message.ToolApprovalResponseContent] must be routed back to
	// this executor as a workflow message.
	//
	// In both modes the agent is re-invoked with the response merged into
	// the conversation, and the [workflow.TurnToken] propagated downstream
	// after the turn is held until all outstanding requests are resolved.
	InterceptUserInputRequests bool

	// InterceptUnterminatedFunctionCalls controls how
	// [message.FunctionCallContent] produced by the agent without a
	// matching [message.FunctionResultContent] in the same turn is
	// dispatched.
	//
	// When false (the default), each unresolved call is raised as a
	// workflow [workflow.ExternalRequest] via
	// [workflow.Context.PostRequest], and the matching
	// [workflow.ExternalResponse] is delivered back to this executor by
	// the runner.
	//
	// When true, each unresolved call is sent as a regular workflow
	// message (via [workflow.Context.SendMessage]); the matching
	// [message.FunctionResultContent] must be routed back to this
	// executor as a workflow message.
	//
	// In both modes the agent is re-invoked with the result merged into
	// the conversation, and the [workflow.TurnToken] propagated downstream
	// after the turn is held until all outstanding calls are resolved.
	InterceptUnterminatedFunctionCalls bool
}

// agentBindingMarker is an unexported sentinel type used as the
// ExecutorBinding.ExecutorType for bindings created by [New].
//
// Using a private type ensures that bindings produced by this package can be
// distinguished from any third-party ExecutorBinding referencing the same
// agent ID, so the workflow builder will surface a clear "different type"
// error instead of silently merging incompatible bindings.
type agentBindingMarker struct{}

// New creates a workflow [workflow.ExecutorBinding] that hosts the given
// [agent.Agent] using the supplied [Config]. The zero value of [Config] is a
// sensible default.
func New(a *agent.Agent, cfg Config) *workflow.ExecutorBinding {
	id := descriptiveID(a)
	userInputPort, functionCallPort := hostPorts(id)
	return &workflow.ExecutorBinding{
		ID:           id,
		ExecutorType: reflect.TypeFor[agentBindingMarker](),
		Raw:          a,
		Ports:        []workflow.RequestPort{userInputPort, functionCallPort},
		NewExecutor: func(_ string) (*workflow.Executor, error) {
			return newHostExecutor(a, cfg).executor(), nil
		},
		// Each run gets its own executor instance via NewExecutor so
		// per-instance turn state is not shared across runs.
		SupportsConcurrentSharedExecution: true,
	}
}

func hostPorts(id string) (userInput, functionCall workflow.RequestPort) {
	userInput = workflow.RequestPort{
		ID:       id + "_UserInput",
		Request:  reflect.TypeFor[*message.ToolApprovalRequestContent](),
		Response: reflect.TypeFor[*message.ToolApprovalResponseContent](),
	}
	functionCall = workflow.RequestPort{
		ID:       id + "_FunctionCall",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[*message.FunctionResultContent](),
	}
	return
}

// hostExecutor implements an [agent.Agent] hosted as a workflow executor.
// All per-run state (buffered turn messages, pending request bookkeeping,
// agent session) is kept on this struct.
type hostExecutor struct {
	id       string
	selfName string
	agent    *agent.Agent
	cfg      Config

	// Ports used to raise external requests when the corresponding
	// Intercept* flag is false (the default).
	userInputPort    workflow.RequestPort
	functionCallPort workflow.RequestPort

	mu       sync.Mutex
	session  agent.Session
	buffered []*message.Message
	// pendingApprovals tracks ToolApprovalRequestContent IDs that have
	// been dispatched and are awaiting a matching response.
	pendingApprovals map[string]struct{}
	// pendingCalls tracks FunctionCallContent CallIDs that have been
	// dispatched and are awaiting a matching FunctionResultContent.
	pendingCalls map[string]struct{}
	// pendingTurn, when non-nil, is the TurnToken whose downstream
	// propagation is being held until all outstanding requests resolve.
	pendingTurn *workflow.TurnToken
	// currentTurnEmitUpdates is the effective EmitUpdates setting for the
	// in-flight turn. It applies to subsequent agent re-invocations
	// triggered by intercepted-request responses.
	currentTurnEmitUpdates bool
}

func newHostExecutor(a *agent.Agent, cfg Config) *hostExecutor {
	id := descriptiveID(a)
	userInputPort, functionCallPort := hostPorts(id)
	return &hostExecutor{
		id:               id,
		selfName:         a.Name(),
		agent:            a,
		cfg:              cfg,
		userInputPort:    userInputPort,
		functionCallPort: functionCallPort,
		pendingApprovals: make(map[string]struct{}),
		pendingCalls:     make(map[string]struct{}),
	}
}

func (h *hostExecutor) executor() *workflow.Executor {
	return &workflow.Executor{
		ID: h.id,
		Options: workflow.ExecutorOptions{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
		},
		Config: []*workflow.ExecutorConfig{
			{
				OnCheckpoint: func(wctx *workflow.Context) error {
					h.mu.Lock()
					session := h.session
					emit := h.currentTurnEmitUpdates
					approvals := slices.Collect(maps.Keys(h.pendingApprovals))
					calls := slices.Collect(maps.Keys(h.pendingCalls))
					buffered := slices.Clone(h.buffered)
					h.mu.Unlock()
					if session != nil {
						data, err := h.agent.MarshalSession(wctx, session)
						if err != nil {
							return err
						}
						if err := wctx.QueueStateUpdate(agentSessionStateKey, "", data); err != nil {
							return err
						}
					}
					if err := wctx.QueueStateUpdate(agentTurnEmitStateKey, "", emit); err != nil {
						return err
					}
					if err := wctx.QueueStateUpdate(agentBufferedStateKey, "", buffered); err != nil {
						return err
					}
					if err := wctx.QueueStateUpdate(pendingApprovalsStateKey, "", approvals); err != nil {
						return err
					}
					return wctx.QueueStateUpdate(pendingCallsStateKey, "", calls)
				},
				OnCheckpointRestored: func(wctx *workflow.Context) error {
					if data, err := wctx.ReadState(agentSessionStateKey, ""); err != nil {
						return err
					} else if data != nil {
						session, err := h.agent.UnmarshalSession(wctx, data.([]byte))
						if err != nil {
							return err
						}
						h.mu.Lock()
						h.session = session
						h.mu.Unlock()
					}
					if v, err := wctx.ReadState(agentTurnEmitStateKey, ""); err != nil {
						return err
					} else if v != nil {
						if b, ok := v.(bool); ok {
							h.mu.Lock()
							h.currentTurnEmitUpdates = b
							h.mu.Unlock()
						}
					}
					if v, err := wctx.ReadState(agentBufferedStateKey, ""); err != nil {
						return err
					} else if v != nil {
						if msgs, ok := v.([]*message.Message); ok {
							h.mu.Lock()
							h.buffered = msgs
							h.mu.Unlock()
						}
					}
					if v, err := wctx.ReadState(pendingApprovalsStateKey, ""); err != nil {
						return err
					} else if v != nil {
						if ks, ok := v.([]string); ok {
							h.mu.Lock()
							h.pendingApprovals = setFromKeys(ks)
							h.mu.Unlock()
						}
					}
					if v, err := wctx.ReadState(pendingCallsStateKey, ""); err != nil {
						return err
					} else if v != nil {
						if ks, ok := v.([]string); ok {
							h.mu.Lock()
							h.pendingCalls = setFromKeys(ks)
							h.mu.Unlock()
						}
					}
					return nil
				},
				ConfigureRoutes: h.configureRoutes,
			},
		},
	}
}

func (h *hostExecutor) configureRoutes(rb *workflow.RouteBuilder) (*workflow.RouteBuilder, error) {
	rb = rb.
		AddHandler(reflect.TypeFor[*message.Message](), nil, false, func(_ *workflow.Context, msg any) (any, error) {
			h.appendMessages(msg.(*message.Message))
			return nil, nil
		}).
		AddHandler(reflect.TypeFor[[]*message.Message](), nil, false, func(_ *workflow.Context, msgs any) (any, error) {
			h.appendMessages(msgs.([]*message.Message)...)
			return nil, nil
		}).
		AddHandler(reflect.TypeFor[workflow.TurnToken](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
			return nil, h.handleTurnToken(wctx, msg.(workflow.TurnToken))
		})

	// External-response handler is always installed; it dispatches by port
	// ID so it serves as the back-channel for both kinds of port-mode
	// requests. When both flags are true (i.e. neither port is used) the
	// handler simply never fires.
	rb = rb.AddHandler(reflect.TypeFor[*workflow.ExternalResponse](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
		return nil, h.handleExternalResponse(wctx, msg.(*workflow.ExternalResponse))
	})

	// Workflow-message response handlers are only installed when the
	// matching flag is true.
	if h.cfg.InterceptUserInputRequests {
		rb = rb.AddHandler(reflect.TypeFor[*message.ToolApprovalResponseContent](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
			return nil, h.handleApprovalResponse(wctx, msg.(*message.ToolApprovalResponseContent))
		})
	}
	if h.cfg.InterceptUnterminatedFunctionCalls {
		rb = rb.AddHandler(reflect.TypeFor[*message.FunctionResultContent](), nil, false, func(wctx *workflow.Context, msg any) (any, error) {
			return nil, h.handleFunctionResult(wctx, msg.(*message.FunctionResultContent))
		})
	}

	return rb, nil
}

func (h *hostExecutor) appendMessages(msgs ...*message.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range msgs {
		if m != nil {
			h.buffered = append(h.buffered, m)
		}
	}
}

// drainBuffered returns the currently buffered messages and resets the buffer.
func (h *hostExecutor) drainBuffered() []*message.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	msgs := h.buffered
	h.buffered = nil
	return msgs
}

// hasOutstandingRequests reports whether any dispatched requests are awaiting
// a response.
func (h *hostExecutor) hasOutstandingRequests() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pendingApprovals)+len(h.pendingCalls) > 0
}

func (h *hostExecutor) handleTurnToken(wctx *workflow.Context, token workflow.TurnToken) error {
	emitUpdates := token.EmitEventsOr(h.cfg.EmitUpdateEvents)
	h.mu.Lock()
	h.currentTurnEmitUpdates = emitUpdates
	h.pendingTurn = &token
	h.mu.Unlock()
	return h.runAgentAndDispatch(wctx)
}

func (h *hostExecutor) handleApprovalResponse(wctx *workflow.Context, resp *message.ToolApprovalResponseContent) error {
	h.mu.Lock()
	if _, ok := h.pendingApprovals[resp.RequestID]; !ok {
		h.mu.Unlock()
		return fmt.Errorf("workflowhosting: no pending FunctionApprovalRequest with ID %q", resp.RequestID)
	}
	delete(h.pendingApprovals, resp.RequestID)
	h.mu.Unlock()
	wrapped := &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{resp},
	}
	h.appendMessages(wrapped)
	return h.runAgentAndDispatch(wctx)
}

func (h *hostExecutor) handleFunctionResult(wctx *workflow.Context, result *message.FunctionResultContent) error {
	h.mu.Lock()
	if _, ok := h.pendingCalls[result.CallID]; !ok {
		h.mu.Unlock()
		return fmt.Errorf("workflowhosting: no pending FunctionCall with CallID %q", result.CallID)
	}
	delete(h.pendingCalls, result.CallID)
	h.mu.Unlock()
	wrapped := &message.Message{
		Role:       message.RoleTool,
		AuthorName: h.selfName,
		Contents:   []message.Content{result},
	}
	h.appendMessages(wrapped)
	return h.runAgentAndDispatch(wctx)
}

// handleExternalResponse routes a port-mode response back to the appropriate
// content-typed handler.
func (h *hostExecutor) handleExternalResponse(wctx *workflow.Context, resp *workflow.ExternalResponse) error {
	switch resp.PortInfo.PortID {
	case h.userInputPort.ID:
		v, ok := resp.Data.As(h.userInputPort.Response)
		if !ok {
			return fmt.Errorf("workflowhosting: expected %v for user input port, got %T", h.userInputPort.Response, resp.Data.Any())
		}
		return h.handleApprovalResponse(wctx, v.(*message.ToolApprovalResponseContent))
	case h.functionCallPort.ID:
		v, ok := resp.Data.As(h.functionCallPort.Response)
		if !ok {
			return fmt.Errorf("workflowhosting: expected %v for function call port, got %T", h.functionCallPort.Response, resp.Data.Any())
		}
		return h.handleFunctionResult(wctx, v.(*message.FunctionResultContent))
	}
	return nil
}

// runAgentAndDispatch invokes the hosted agent with the buffered turn
// messages, dispatches outputs and any requests, and propagates the held
// TurnToken downstream when no outstanding requests remain.
func (h *hostExecutor) runAgentAndDispatch(wctx *workflow.Context) error {
	messages := h.drainBuffered()

	if !h.cfg.DisableMessageForwarding && len(messages) > 0 {
		if err := wctx.SendMessage("", messages); err != nil {
			return err
		}
	}

	agentInput := messages
	if !h.cfg.DisableRoleReassignment {
		agentInput = reassignOtherAgentsAsUsers(messages, h.selfName)
	}

	session, err := h.ensureSession(wctx)
	if err != nil {
		return err
	}

	h.mu.Lock()
	emitUpdates := h.currentTurnEmitUpdates
	h.mu.Unlock()

	runOpts := []agent.Option{
		agent.WithSession(session),
		// Run the agent in streaming mode only when update events are to be emitted.
		agent.Stream(emitUpdates),
	}

	var resp agent.Response
	for update, err := range h.agent.Run(wctx, agentInput, runOpts...) {
		if err != nil {
			return err
		}
		if emitUpdates {
			if err := wctx.AddEvent(workflow.OutputEvent{ExecutorID: h.id, Output: update}); err != nil {
				return err
			}
		}
		resp.Update(update)
	}
	resp.Coalesce()

	// Stamp this hosting executor's name on every aggregated message,
	// so the role-reassignment logic in receiving hosts works correctly.
	resp.AgentID = h.agent.ID()
	for _, m := range resp.Messages {
		m.AuthorName = h.selfName
	}

	if h.cfg.EmitResponseEvents {
		if err := wctx.AddEvent(workflow.OutputEvent{ExecutorID: h.id, Output: &resp}); err != nil {
			return err
		}
	}

	// Filter out server-side artifacts (reasoning tokens, web search calls, etc.)
	// that are internal to this agent. Forwarding them to other agents in the
	// workflow can cause invalid request errors when the receiving agent uses an
	// API that does not accept those output-only item types as input.
	if forwardableMessages := filterForwardableMessages(resp.Messages); len(forwardableMessages) > 0 {
		if err := wctx.SendMessage("", forwardableMessages); err != nil {
			return err
		}
	}

	if err := h.dispatchRequests(wctx, resp.Messages); err != nil {
		return err
	}

	// Only release the held TurnToken downstream once all outstanding
	// requests have been resolved.
	if !h.hasOutstandingRequests() {
		h.mu.Lock()
		held := h.pendingTurn
		had := held != nil
		emit := h.currentTurnEmitUpdates
		h.pendingTurn = nil
		h.currentTurnEmitUpdates = false
		h.mu.Unlock()
		if had {
			// Forward a fresh TurnToken stamped with the resolved
			// EmitEvents value so downstream executors observe the
			// effective per-turn setting (not the possibly-nil input).
			return wctx.SendMessage("", workflow.TurnToken{EmitEvents: &emit})
		}
	}
	return nil
}

// filterForwardableMessages filters response messages to only include portable
// conversational content, and strips Message.RawRepresentation so
// provider-specific output items (for example mcp_list_tools, reasoning, or
// web_search_call payloads) are not round-tripped as input to another agent.
func filterForwardableMessages(messages []*message.Message) []*message.Message {
	result := make([]*message.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		contents := filterForwardableContents(msg.Contents)
		if len(contents) == 0 {
			continue
		}
		clone := msg.Clone()
		clone.RawRepresentation = nil
		clone.Contents = contents
		result = append(result, clone)
	}
	return result
}

// filterForwardableContents keeps content types that represent meaningful
// conversational content portable across agents. Messages containing only
// content types outside this set, such as reasoning tokens or usage metadata,
// are dropped before forwarding.
func filterForwardableContents(contents message.Contents) message.Contents {
	result := make(message.Contents, 0, len(contents))
	for _, content := range contents {
		switch content.(type) {
		case *message.TextContent,
			*message.DataContent,
			*message.URIContent,
			*message.FunctionCallContent,
			*message.FunctionResultContent,
			*message.ToolApprovalRequestContent,
			*message.ToolApprovalResponseContent,
			*message.HostedFileContent,
			*message.ErrorContent:
			result = append(result, content)
		}
	}
	return result
}

// dispatchRequests scans the agent's response for request content and
// dispatches each one — either as a workflow message (when the corresponding
// Intercept* flag is true) or as an external request via PostRequest (when
// the flag is false, the default).
//
// Within a single response, duplicate IDs are an error. A request whose ID is
// already pending from a previous response (e.g. a re-emission) is silently
// skipped (idempotent) so the executor doesn't double-dispatch.
func (h *hostExecutor) dispatchRequests(wctx *workflow.Context, msgs []*message.Message) error {
	// Pre-compute the set of FunctionCallContent CallIDs that already have a
	// matching FunctionResultContent in the same response, so they are not
	// considered unresolved.
	resolved := make(map[string]struct{})
	for _, m := range msgs {
		for _, c := range m.Contents {
			if r, ok := c.(*message.FunctionResultContent); ok {
				resolved[r.CallID] = struct{}{}
			}
		}
	}

	// Track IDs seen so far in this single dispatch to detect within-response
	// duplicates as errors.
	seenApprovals := make(map[string]struct{})
	seenCalls := make(map[string]struct{})

	for _, m := range msgs {
		for _, c := range m.Contents {
			switch v := c.(type) {
			case *message.ToolApprovalRequestContent:
				if _, dup := seenApprovals[v.RequestID]; dup {
					return fmt.Errorf("workflowhosting: duplicate ToolApprovalRequest  ID %q in same response", v.RequestID)
				}
				seenApprovals[v.RequestID] = struct{}{}
				h.mu.Lock()
				if _, alreadyPending := h.pendingApprovals[v.RequestID]; alreadyPending {
					h.mu.Unlock()
					// Already-pending request: idempotent re-emission, no-op.
					continue
				}
				h.pendingApprovals[v.RequestID] = struct{}{}
				h.mu.Unlock()
				if h.cfg.InterceptUserInputRequests {
					if err := wctx.SendMessage("", v); err != nil {
						return err
					}
				} else {
					req, err := workflow.NewExternalRequest(h.userInputPort.ID+":"+v.RequestID, h.userInputPort, v)
					if err != nil {
						return err
					}
					if err := wctx.PostRequest(req); err != nil {
						return err
					}
				}
			case *message.FunctionCallContent:
				if _, done := resolved[v.CallID]; done {
					continue
				}
				if _, dup := seenCalls[v.CallID]; dup {
					return fmt.Errorf("workflowhosting: duplicate FunctionCall CallID %q in same response", v.CallID)
				}
				seenCalls[v.CallID] = struct{}{}
				h.mu.Lock()
				if _, alreadyPending := h.pendingCalls[v.CallID]; alreadyPending {
					h.mu.Unlock()
					// Already-pending call: idempotent re-emission, no-op.
					continue
				}
				h.pendingCalls[v.CallID] = struct{}{}
				h.mu.Unlock()
				if h.cfg.InterceptUnterminatedFunctionCalls {
					if err := wctx.SendMessage("", v); err != nil {
						return err
					}
				} else {
					req, err := workflow.NewExternalRequest(h.functionCallPort.ID+":"+v.CallID, h.functionCallPort, v)
					if err != nil {
						return err
					}
					if err := wctx.PostRequest(req); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (h *hostExecutor) ensureSession(ctx context.Context) (agent.Session, error) {
	h.mu.Lock()
	if h.session != nil {
		s := h.session
		h.mu.Unlock()
		return s, nil
	}
	h.mu.Unlock()
	s, err := h.agent.CreateSession(ctx)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.session == nil {
		h.session = s
	} else {
		s = h.session
	}
	h.mu.Unlock()
	return s, nil
}

// reassignOtherAgentsAsUsers returns a copy of msgs in which any RoleAssistant
// message whose AuthorName does not match selfName is rewritten with RoleUser.
// Messages that are unchanged are returned by the original pointer (no copy).
func reassignOtherAgentsAsUsers(msgs []*message.Message, selfName string) []*message.Message {
	var out []*message.Message
	for i, m := range msgs {
		if m == nil || m.Role != message.RoleAssistant || m.AuthorName == selfName {
			if out != nil {
				out = append(out, m)
			}
			continue
		}
		if out == nil {
			out = make([]*message.Message, 0, len(msgs))
			out = append(out, msgs[:i]...)
		}
		clone := m.Clone()
		clone.Role = message.RoleUser
		out = append(out, clone)
	}
	if out == nil {
		return msgs
	}
	return out
}

func descriptiveID(a *agent.Agent) string {
	if a.Name() != "" {
		return a.Name() + "_" + a.ID()
	}
	return a.ID()
}

func setFromKeys(ks []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ks))
	for _, k := range ks {
		out[k] = struct{}{}
	}
	return out
}
