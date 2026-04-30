// Copyright (c) Microsoft. All rights reserved.

// Package workflowprovider hosts a [workflow.Workflow] as an [agent.Agent].
//
// The first agent run on a session opens a streaming workflow run; subsequent
// runs reuse it. Workflow events are translated into agent
// [message.ResponseUpdate]s as the workflow executes. Pending external
// requests raised by the workflow (via [workflow.RequestInfoEvent]) are
// surfaced as response updates carrying the request content; the caller can
// then resume by including the matching response content (e.g.
// [message.FunctionResultContent] or [message.FunctionApprovalResponseContent])
// in the next agent run, and the provider routes them as
// [workflow.ExternalResponse]s.
package workflowprovider

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var messagesSliceType = reflect.TypeFor[[]*message.Message]()

const sessionStateKey = "workflowprovider_state"

// providerServiceID marks sessions managed by this provider. Setting a
// non-empty ServiceID on the session opts out of the agent package's
// default in-memory chat history middleware, which would otherwise
// replay prior messages into the workflow on every call. The workflow
// itself owns conversational state across turns.
const providerServiceID = "workflowprovider"

// Config configures a [workflow.Workflow] hosted as an [agent.Agent] via [New].
type Config struct {
	agent.Config

	// Environment is the execution environment used to run the workflow on
	// each agent turn. Defaults to [inproc.Default] when nil.
	Environment *inproc.ExecutionEnvironment

	// IncludeOutputsInResponse, if true, surfaces [workflow.OutputEvent]
	// payloads in the agent response stream when the payload is a
	// [*message.Message] or [[]*message.Message]. By default outputs are
	// observed only via [workflow.ResponseUpdateEvent]s emitted by hosted
	// agents inside the workflow.
	IncludeOutputsInResponse bool

	// IncludeErrorDetails, if true, surfaces the full error message from
	// [workflow.ErrorEvent]s in the agent response stream. When false, a
	// generic message is emitted instead.
	IncludeErrorDetails bool
}

// pendingReq records a pending external request raised by the workflow so
// that a matching response content in a subsequent agent run can be
// translated back to a [workflow.ExternalResponse].
type pendingReq struct {
	port              workflow.RequestPort
	externalRequestID string
}

// providerState holds the workflow run state that survives across multiple
// [agent.Agent.Run] invocations on the same session. It is stored on the
// [agent.Session] under [sessionStateKey].
//
// The streaming run is an in-memory object and is not portable across
// process boundaries; sessions that are persisted via
// [agent.Agent.MarshalSession] retain the request-tracking metadata but
// drop the live run.
type providerState struct {
	stream  workflow.StreamingRun
	pending map[string]pendingReq // keyed by request content ID (e.g. CallID/RequestID)
}

// New wraps a [*workflow.Workflow] as an [*agent.Agent].
//
// The workflow's start executor must accept [[]*message.Message] (typically
// configured via [messageworkflow.NewExecutorConfig]). On the first call to
// the agent's Run for a given session, a fresh streaming run is started.
// Subsequent calls reuse that run, sending follow-up messages and
// [workflow.ExternalResponse]s.
func New(wf *workflow.Workflow, cfg Config) (*agent.Agent, error) {
	if wf == nil {
		return nil, errors.New("workflow cannot be nil")
	}
	descriptor, err := wf.DescribeProtocol()
	if err != nil {
		return nil, fmt.Errorf("workflow start executor protocol could not be determined: %w", err)
	}
	// Validate that the start executor can accept the messages we'll send.
	if !slices.Contains(descriptor.Accepts, messagesSliceType) {
		return nil, fmt.Errorf("workflow start executor does not accept []*message.Message")
	}

	env := cfg.Environment
	if env == nil {
		if wf.AllowConcurrent() {
			env = inproc.Concurrent
		} else {
			env = inproc.OffThread
		}
	}

	runFn := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			sess, _ := agent.GetOption(options, agent.WithSession)
			if sess != nil && sess.ServiceID() == "" {
				sess.SetServiceID(providerServiceID)
			}

			state, err := loadOrInitState(ctx, sess, env, wf)
			if err != nil {
				yield(nil, err)
				return
			}
			defer saveState(sess, state)

			// Split incoming messages into ExternalResponses for pending
			// requests and the remaining workflow messages.
			remaining, responses, hasMatchedStartResponse := splitResponses(messages, state.pending, wf.StartExecutorID, state.stream.ResponsePortExecutorID)

			for _, resp := range responses {
				if err := state.stream.SendResponse(ctx, resp); err != nil {
					yield(nil, err)
					return
				}
			}
			if len(remaining) > 0 {
				if err := state.stream.SendMessage(ctx, remaining); err != nil {
					yield(nil, err)
					return
				}
			}
			// Suppress the TurnToken only when the only activity is a
			// matched external response addressed to the start executor.
			// The start executor's response handler self-emits a
			// TurnToken when it completes its turn, so an extra one
			// would drive an additional agent turn (mirrors .NET's
			// WorkflowSession.shouldSendTurnToken logic). Non-start
			// owners (e.g. RequestPort executors) do not self-emit a
			// TurnToken, so we still need to provide one.
			shouldSendTurnToken := len(responses) == 0 || !hasMatchedStartResponse || len(remaining) > 0
			if shouldSendTurnToken {
				emit := true
				if err := state.stream.SendMessage(ctx, workflow.TurnToken{EmitEvents: &emit}); err != nil {
					yield(nil, err)
					return
				}
			}

			for evt, err := range state.stream.WatchUntilHalt(ctx) {
				if err != nil {
					yield(nil, err)
					return
				}
				switch e := evt.(type) {
				case workflow.ResponseUpdateEvent:
					if !yield(e.Update, nil) {
						return
					}
				case workflow.ResponseEvent:
					if e.Response == nil {
						continue
					}
					for _, msg := range e.Response.Messages {
						if !yield(messageToUpdate(msg), nil) {
							return
						}
					}
				case workflow.OutputEvent:
					if !cfg.IncludeOutputsInResponse {
						continue
					}
					switch out := e.Output.(type) {
					case *message.Message:
						if !yield(messageToUpdate(out), nil) {
							return
						}
					case []*message.Message:
						for _, msg := range out {
							if !yield(messageToUpdate(msg), nil) {
								return
							}
						}
					}
				case workflow.RequestInfoEvent:
					update, contentID, ok := requestToUpdate(e.Request)
					if !ok {
						continue
					}
					state.pending[contentID] = pendingReq{
						port:              e.Request.RequestPort,
						externalRequestID: e.Request.ID,
					}
					if !yield(update, nil) {
						return
					}
				case workflow.ErrorEvent:
					text := "an error occurred while executing the workflow"
					if cfg.IncludeErrorDetails && e.Error != nil {
						text = e.Error.Error()
					}
					update := &message.ResponseUpdate{
						Role: message.RoleAssistant,
						Contents: []message.Content{&message.ErrorContent{
							Message: text,
						}},
					}
					if !yield(update, nil) {
						return
					}
				}
			}
		}
	}

	// The hosted workflow handles its own tool-call loop via its inner
	// executors, so the agent-level autocall middleware would double-process
	// function calls.
	cfg.DisableFuncAutoCall = true
	return agent.New(
		agent.ProviderConfig{
			ProviderName: "workflow",
			Run:          runFn,
		},
		cfg.Config,
	), nil
}

// loadOrInitState fetches the per-session [providerState], creating a fresh
// streaming workflow run on first use.
func loadOrInitState(
	ctx context.Context,
	sess agent.Session,
	env *inproc.ExecutionEnvironment,
	wf *workflow.Workflow,
) (*providerState, error) {
	if sess != nil {
		var state *providerState
		if ok, _ := sess.Get(sessionStateKey, &state); ok && state != nil && state.stream != nil {
			if state.pending == nil {
				state.pending = make(map[string]pendingReq)
			}
			return state, nil
		}
	}
	stream, err := env.OpenStream(ctx, wf, "")
	if err != nil {
		return nil, err
	}
	return &providerState{stream: stream, pending: make(map[string]pendingReq)}, nil
}

func saveState(sess agent.Session, state *providerState) {
	if sess == nil || state == nil {
		return
	}
	sess.Set(sessionStateKey, state)
}

// splitResponses scans messages for response content matching pending
// requests. Matched contents are converted to [workflow.ExternalResponse]s
// and removed from their messages; the remaining messages (with their
// non-matching contents) are returned for forwarding into the workflow as
// regular workflow input.
//
// hasMatchedStartResponse indicates whether at least one matched response
// was bound to a request whose port is owned by the workflow's start
// executor. Callers use this to decide whether to send a [workflow.TurnToken]
// alongside the responses (the start executor's response handler self-emits
// a TurnToken when it completes, so an extra one would cause a duplicate
// turn). Mirrors .NET's [WorkflowSession.HasMatchedResponseForStartExecutor].
func splitResponses(
	messages []*message.Message,
	pending map[string]pendingReq,
	startExecutorID string,
	lookupPortOwner func(portID string) (string, bool),
) ([]*message.Message, []*workflow.ExternalResponse, bool) {
	if len(messages) == 0 || len(pending) == 0 {
		return messages, nil, false
	}
	var (
		responses               []*workflow.ExternalResponse
		hasMatchedStartResponse bool
	)
	out := make([]*message.Message, 0, len(messages))
	for _, m := range messages {
		if m == nil {
			continue
		}
		var keep []message.Content
		for _, c := range m.Contents {
			if id, ok := responseContentID(c); ok {
				if pr, found := pending[id]; found {
					responses = append(responses, &workflow.ExternalResponse{
						RequestID:   pr.externalRequestID,
						RequestPort: pr.port,
						Data:        workflow.AnyPortableValue(c),
					})
					if owner, ok := lookupPortOwner(pr.port.ID); ok && owner == startExecutorID {
						hasMatchedStartResponse = true
					}
					delete(pending, id)
					continue
				}
			}
			keep = append(keep, c)
		}
		if len(keep) > 0 {
			cloned := *m
			cloned.Contents = keep
			out = append(out, &cloned)
		}
	}
	return out, responses, hasMatchedStartResponse
}

// responseContentID returns the matching key for a content item if it is a
// known response type.
func responseContentID(c message.Content) (string, bool) {
	switch v := c.(type) {
	case *message.FunctionResultContent:
		return v.CallID, true
	case *message.FunctionApprovalResponseContent:
		return v.ID, true
	}
	return "", false
}

// requestToUpdate translates an [*workflow.ExternalRequest] into a
// [*message.ResponseUpdate] that surfaces the request content to the caller.
// The second return value is the request content's matching key (to track
// in the pending map).
func requestToUpdate(req *workflow.ExternalRequest) (*message.ResponseUpdate, string, bool) {
	if req == nil {
		return nil, "", false
	}
	v, ok := req.Data.As(req.RequestPort.Request)
	if !ok {
		return nil, "", false
	}
	c, ok := v.(message.Content)
	if !ok {
		return nil, "", false
	}
	id, ok := requestContentID(c)
	if !ok {
		return nil, "", false
	}
	return &message.ResponseUpdate{
		Role:     message.RoleAssistant,
		Contents: []message.Content{c},
	}, id, true
}

// requestContentID returns the matching key for a request content item.
func requestContentID(c message.Content) (string, bool) {
	switch v := c.(type) {
	case *message.FunctionCallContent:
		return v.CallID, true
	case *message.FunctionApprovalRequestContent:
		return v.ID, true
	}
	return "", false
}

func messageToUpdate(m *message.Message) *message.ResponseUpdate {
	if m == nil {
		return &message.ResponseUpdate{}
	}
	return &message.ResponseUpdate{
		Role:       m.Role,
		Contents:   m.Contents,
		AuthorID:   m.AuthorID,
		AuthorName: m.AuthorName,
		MessageID:  m.ID,
	}
}
