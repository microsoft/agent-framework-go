// Copyright (c) Microsoft. All rights reserved.

// This file hosts an [agent.Agent] as a workflow [workflow.Executor], so the
// agent can participate in graphs alongside regular executors and other hosted
// agents.
package agentworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/contentexthandler"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messageworkflow"
	"github.com/microsoft/agent-framework-go/workflow"
)

const (
	agentHostStateKey     = "AIAgentHostState"
	agentBufferedStateKey = "AIAgentHostExecutor.State"
	agentHostBindingID    = "agentworkflow.Agent"
	userInputHandlerID    = "_userInputHandler"
	functionCallHandlerID = "_functionCallHandler"
)

var invalidDescriptiveIDChars = regexp.MustCompile(`[^0-9A-Za-z]+`)

type agentHostState struct {
	ThreadState           []byte
	CurrentTurnEmitEvents *bool
}

// ResetSignal notifies an agent-hosting executor to reset its agent
// conversation state and start a new session when appropriate.
type ResetSignal struct{}

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

	// DisableForwardIncomingMessages disables forwarding of incoming messages
	// downstream before the agent runs. By default (zero value), incoming
	// messages are forwarded so downstream nodes observe the full
	// conversation. Set to true for strict pipelines where each node should
	// only forward its own output.
	DisableForwardIncomingMessages bool

	// DisableReassignOtherAgentsAsUsers disables rewriting incoming
	// [message.RoleAssistant] messages whose [message.Message.AuthorName]
	// does not match this agent to [message.RoleUser]. By default (zero
	// value), such messages are reassigned so the conversation between
	// agents appears, to each agent, as messages from "the user". Set to
	// true to preserve original roles.
	DisableReassignOtherAgentsAsUsers bool

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

// New creates a workflow [workflow.ExecutorBinding] that hosts the given
// [agent.Agent] using the supplied [Config]. The zero value of [Config] is a
// sensible default.
func New(a *agent.Agent, cfg Config) workflow.ExecutorBinding {
	id := descriptiveID(a)
	ports := hostPorts(id)
	return workflow.ExecutorBinding{
		ID:               id,
		ImplementationID: agentHostBindingID,
		RawValue:         a,
		Ports:            ports.asSlice(),
		NewExecutorFunc: func(_ string) (*workflow.Executor, error) {
			return newHostExecutor(a, cfg).executor(), nil
		},
		SupportsConcurrentSharedExecution: true,
	}
}

type hostPortSet struct {
	userInput    workflow.RequestPort
	functionCall workflow.RequestPort
}

func hostPorts(id string) hostPortSet {
	return hostPortSet{
		userInput: workflow.RequestPort{
			ID:       id + "_UserInput",
			Request:  reflect.TypeFor[*message.ToolApprovalRequestContent](),
			Response: reflect.TypeFor[*message.ToolApprovalResponseContent](),
		},
		functionCall: workflow.RequestPort{
			ID:       id + "_FunctionCall",
			Request:  reflect.TypeFor[*message.FunctionCallContent](),
			Response: reflect.TypeFor[*message.FunctionResultContent](),
		},
	}
}

func (p hostPortSet) asSlice() []workflow.RequestPort {
	return []workflow.RequestPort{p.userInput, p.functionCall}
}

// hostExecutor implements an [agent.Agent] hosted as a workflow executor.
// All per-run state (buffered turn messages, pending request bookkeeping,
// agent session) is kept on this struct.
type hostExecutor struct {
	id    string
	agent *agent.Agent
	cfg   Config

	session *agent.Session

	messageState    *messageworkflow.MessageState
	approvalHandler *contentexthandler.Handler[*message.ToolApprovalRequestContent, *message.ToolApprovalResponseContent]
	callHandler     *contentexthandler.Handler[*message.FunctionCallContent, *message.FunctionResultContent]
	turnEmitEvents  *bool
}

func newHostExecutor(a *agent.Agent, cfg Config) *hostExecutor {
	id := descriptiveID(a)
	ports := hostPorts(id)
	h := &hostExecutor{
		id:           id,
		agent:        a,
		cfg:          cfg,
		messageState: messageworkflow.NewMessageState(agentBufferedStateKey, ""),
	}
	h.approvalHandler = contentexthandler.New(contentexthandler.Options[*message.ToolApprovalRequestContent, *message.ToolApprovalResponseContent]{
		Port:              ports.userInput,
		PendingRequestsID: userInputHandlerID,
		Intercepted:       cfg.InterceptUserInputRequests,
		RequestID:         func(req *message.ToolApprovalRequestContent) string { return req.RequestID },
		ResponseID:        func(resp *message.ToolApprovalResponseContent) string { return resp.RequestID },
		ResponseHandler:   h.handleApprovalResponse,
	})
	h.callHandler = contentexthandler.New(contentexthandler.Options[*message.FunctionCallContent, *message.FunctionResultContent]{
		Port:              ports.functionCall,
		PendingRequestsID: functionCallHandlerID,
		Intercepted:       cfg.InterceptUnterminatedFunctionCalls,
		RequestID:         func(req *message.FunctionCallContent) string { return req.CallID },
		ResponseID:        func(resp *message.FunctionResultContent) string { return resp.CallID },
		ResponseHandler:   h.handleFunctionResult,
	})
	return h
}

func (h *hostExecutor) executor() *workflow.Executor {
	executor := workflow.Executor{ID: h.id}
	messageworkflow.Configure(&executor, &messageworkflow.Options{
		StateKey:                 agentBufferedStateKey,
		TakeTurnHandler:          h.handleTurnToken,
		StringMessageRole:        string(message.RoleUser),
		DisableAutoSendTurnToken: true,
		MessageState:             h.messageState,
	})
	executor.Extend(&workflow.Executor{
		OnCheckpointFunc:         h.onCheckpoint,
		OnCheckpointRestoredFunc: h.onCheckpointRestored,
		ConfigureProtocol:        h.configureRoutes,
	})
	return &executor
}

func (h *hostExecutor) onCheckpoint(wctx *workflow.Context) error {
	state := agentHostState{CurrentTurnEmitEvents: h.turnEmitEvents}
	if h.session != nil {
		data, err := json.Marshal(h.session)
		if err != nil {
			return err
		}
		state.ThreadState = data
	}
	if err := wctx.QueueStateUpdate(agentHostStateKey, "", state); err != nil {
		return err
	}
	if err := h.approvalHandler.Checkpoint(wctx); err != nil {
		return err
	}
	if err := h.callHandler.Checkpoint(wctx); err != nil {
		return err
	}
	return nil
}

func (h *hostExecutor) onCheckpointRestored(wctx *workflow.Context) error {
	h.session = nil
	h.turnEmitEvents = nil
	if err := h.approvalHandler.Restore(wctx); err != nil {
		return err
	}
	if err := h.callHandler.Restore(wctx); err != nil {
		return err
	}
	data, err := wctx.ReadState(agentHostStateKey, "")
	if err != nil {
		return err
	}
	state, err := agentHostStateFromAny(data)
	if err != nil {
		return err
	}
	if state != nil {
		h.turnEmitEvents = state.CurrentTurnEmitEvents
		if state.ThreadState != nil {
			session := &agent.Session{}
			if err := json.Unmarshal(state.ThreadState, session); err != nil {
				return err
			}
			h.session = session
		}
	}
	return nil
}

func (h *hostExecutor) configureRoutes(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
	pb.SendsMessageType(
		reflect.TypeFor[[]*message.Message](),
		reflect.TypeFor[workflow.TurnToken](),
		reflect.TypeFor[*message.ToolApprovalRequestContent](),
		reflect.TypeFor[*message.FunctionCallContent](),
	)
	rb := &pb.RouteBuilder
	rb = h.approvalHandler.ConfigureRoutes(rb)
	rb = h.callHandler.ConfigureRoutes(rb)
	pb.RouteBuilder = *rb
	rb = rb.AddHandlerRaw(reflect.TypeFor[ResetSignal](), nil, func(wctx *workflow.Context, msg any) (any, error) {
		h.session = nil
		h.turnEmitEvents = nil
		return nil, nil
	})

	// External-response handler is always installed; it dispatches by port
	// ID so it serves as the back-channel for both kinds of port-mode
	// requests. When both flags are true (i.e. neither port is used) the
	// handler simply never fires.
	rb = rb.AddHandlerRaw(reflect.TypeFor[*workflow.ExternalResponse](), nil, func(wctx *workflow.Context, msg any) (any, error) {
		return nil, h.handleExternalResponse(wctx, msg.(*workflow.ExternalResponse))
	})

	pb.RouteBuilder = *rb
	return pb, nil
}

// drainBuffered returns the currently buffered messages and resets the buffer.
func (h *hostExecutor) drainBuffered(wctx *workflow.Context) ([]*message.Message, error) {
	var messages []*message.Message
	err := h.messageState.ProcessTurnMessages(wctx, func(_ *workflow.Context, buffered []*message.Message) ([]*message.Message, error) {
		messages = buffered
		return nil, nil
	})
	return messages, err
}

func (h *hostExecutor) handleTurnToken(wctx *workflow.Context, token workflow.TurnToken, messages []*message.Message) error {
	emitUpdates := token.EmitEventsOr(h.cfg.EmitUpdateEvents)
	h.turnEmitEvents = &emitUpdates
	return h.runAgentAndDispatch(wctx, messages)
}

func (h *hostExecutor) handleApprovalResponse(wctx *workflow.Context, resp *message.ToolApprovalResponseContent) error {
	wrapped := &message.Message{
		ID:        newMessageID(),
		Role:      message.RoleUser,
		Contents:  []message.Content{resp},
		CreatedAt: time.Now().UTC(),
	}
	found, err := h.approvalHandler.MarkRequestAsHandled(wctx, resp)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("agentworkflow: no pending tool approval request with id %q", resp.RequestID)
	}
	if err := h.messageState.ProcessTurnMessages(wctx, func(_ *workflow.Context, buffered []*message.Message) ([]*message.Message, error) {
		return append(buffered, wrapped), nil
	}); err != nil {
		return err
	}
	messages, err := h.drainBuffered(wctx)
	if err != nil {
		return err
	}
	return h.runAgentAndDispatch(wctx, messages)
}

func (h *hostExecutor) handleFunctionResult(wctx *workflow.Context, result *message.FunctionResultContent) error {
	wrapped := &message.Message{
		ID:         newMessageID(),
		Role:       message.RoleTool,
		AuthorName: agentNameOrID(h.agent),
		Contents:   []message.Content{result},
		CreatedAt:  time.Now().UTC(),
	}
	found, err := h.callHandler.MarkRequestAsHandled(wctx, result)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("agentworkflow: no pending function call with id %q", result.CallID)
	}
	if err := h.messageState.ProcessTurnMessages(wctx, func(_ *workflow.Context, buffered []*message.Message) ([]*message.Message, error) {
		return append(buffered, wrapped), nil
	}); err != nil {
		return err
	}
	messages, err := h.drainBuffered(wctx)
	if err != nil {
		return err
	}
	return h.runAgentAndDispatch(wctx, messages)
}

// handleExternalResponse routes a port-mode response back to the appropriate
// content-typed handler.
func (h *hostExecutor) handleExternalResponse(wctx *workflow.Context, resp *workflow.ExternalResponse) error {
	if handled, err := h.approvalHandler.HandleExternalResponse(wctx, resp); handled || err != nil {
		return err
	}
	if handled, err := h.callHandler.HandleExternalResponse(wctx, resp); handled || err != nil {
		return err
	}
	return nil
}

// runAgentAndDispatch invokes the hosted agent with the buffered turn
// messages, dispatches outputs and any requests, and propagates the held
// TurnToken downstream when no outstanding requests remain.
func (h *hostExecutor) runAgentAndDispatch(wctx *workflow.Context, messages []*message.Message) error {
	if !h.cfg.DisableForwardIncomingMessages && len(messages) > 0 {
		if err := wctx.SendMessage("", messages); err != nil {
			return err
		}
	}

	agentInput := messages
	if !h.cfg.DisableReassignOtherAgentsAsUsers {
		agentInput = reassignOtherAgentsAsUsers(messages, agentNameOrID(h.agent))
	}

	session, err := h.ensureSession(wctx)
	if err != nil {
		return err
	}

	emitEvents := h.turnEmitEvents
	emitUpdates := false
	if emitEvents != nil {
		emitUpdates = *emitEvents
	}

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
			if err := wctx.YieldOutput(update); err != nil {
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
		if m.AuthorName == "" {
			m.AuthorName = h.agent.Name()
		}
	}

	if h.cfg.EmitResponseEvents {
		if err := wctx.YieldOutput(&resp); err != nil {
			return err
		}
	}

	if err := h.dispatchRequests(wctx, resp.Messages); err != nil {
		return err
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

	if err := h.releasePendingTurnIfReady(wctx); err != nil {
		return err
	}
	return nil
}

// releasePendingTurnIfReady propagates the held TurnToken downstream once all
// outstanding requests have been resolved.
func (h *hostExecutor) releasePendingTurnIfReady(wctx *workflow.Context) error {
	var emit *bool
	hasApprovals, err := h.approvalHandler.HasPending(wctx)
	if err != nil {
		return err
	}
	hasCalls, err := h.callHandler.HasPending(wctx)
	if err != nil {
		return err
	}
	if !hasApprovals && !hasCalls {
		emit = h.turnEmitEvents
		h.turnEmitEvents = nil
	}
	if emit == nil {
		return nil
	}
	// Forward a fresh TurnToken stamped with the resolved EmitEvents value so
	// downstream executors observe the effective per-turn setting.
	return wctx.SendMessage("", workflow.TurnToken{EmitEvents: emit})
}

func agentHostStateFromAny(value any) (*agentHostState, error) {
	if value == nil {
		return nil, nil
	}
	switch state := value.(type) {
	case agentHostState:
		return &state, nil
	case *agentHostState:
		return state, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var state agentHostState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
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
	approvalRequests := make(map[string]*message.ToolApprovalRequestContent)
	functionCalls := make(map[string]*message.FunctionCallContent)
	var approvalOrder []string
	var callOrder []string

	for _, m := range msgs {
		for _, c := range m.Contents {
			switch v := c.(type) {
			case *message.ToolApprovalRequestContent:
				if _, exists := approvalRequests[v.RequestID]; exists {
					return fmt.Errorf("agentworkflow: duplicate tool approval request id %q", v.RequestID)
				}
				approvalRequests[v.RequestID] = v
				approvalOrder = append(approvalOrder, v.RequestID)
			case *message.ToolApprovalResponseContent:
				delete(approvalRequests, v.RequestID)
			case *message.FunctionCallContent:
				if _, exists := functionCalls[v.CallID]; exists {
					return fmt.Errorf("agentworkflow: duplicate function call id %q", v.CallID)
				}
				functionCalls[v.CallID] = v
				callOrder = append(callOrder, v.CallID)
			case *message.FunctionResultContent:
				delete(functionCalls, v.CallID)
			}
		}
	}

	var approvalDispatches []*message.ToolApprovalRequestContent
	var callDispatches []*message.FunctionCallContent
	for _, id := range approvalOrder {
		approval, ok := approvalRequests[id]
		if !ok {
			continue
		}
		delete(approvalRequests, id)
		added, err := h.approvalHandler.TrackRequest(wctx, approval)
		if err != nil {
			return err
		}
		if added {
			approvalDispatches = append(approvalDispatches, approval)
		}
	}
	for _, id := range callOrder {
		call, ok := functionCalls[id]
		if !ok {
			continue
		}
		delete(functionCalls, id)
		added, err := h.callHandler.TrackRequest(wctx, call)
		if err != nil {
			return err
		}
		if added {
			callDispatches = append(callDispatches, call)
		}
	}

	for _, approval := range approvalDispatches {
		if err := h.approvalHandler.DispatchRequest(wctx, approval); err != nil {
			return err
		}
	}
	for _, call := range callDispatches {
		if err := h.callHandler.DispatchRequest(wctx, call); err != nil {
			return err
		}
	}
	return nil
}

func (h *hostExecutor) ensureSession(ctx context.Context) (*agent.Session, error) {
	if h.session != nil {
		return h.session, nil
	}
	s, err := h.agent.CreateSession(ctx)
	if err != nil {
		return nil, err
	}
	h.session = s
	return s, nil
}

// reassignOtherAgentsAsUsers returns a copy of msgs in which any RoleAssistant
// message whose AuthorName does not match selfName is rewritten with RoleUser.
// Messages that are unchanged are returned by the original pointer (no copy).
func reassignOtherAgentsAsUsers(msgs []*message.Message, selfName string) []*message.Message {
	var out []*message.Message
	for i, m := range msgs {
		if !shouldReassignAssistantMessage(m, selfName) {
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

func shouldReassignAssistantMessage(m *message.Message, selfName string) bool {
	if m == nil || m.Role != message.RoleAssistant || m.AuthorName == selfName {
		return false
	}
	for _, content := range m.Contents {
		switch content.(type) {
		case *message.TextContent, *message.DataContent, *message.URIContent, *message.UsageContent:
		default:
			return false
		}
	}
	return true
}

func agentNameOrID(a *agent.Agent) string {
	if name := a.Name(); name != "" {
		return name
	}
	return a.ID()
}

func newMessageID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func descriptiveID(a *agent.Agent) string {
	id := a.ID()
	if a.Name() != "" {
		id = a.Name() + "_" + id
	}
	return invalidDescriptiveIDChars.ReplaceAllString(id, "_")
}
