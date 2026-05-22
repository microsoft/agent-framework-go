// Copyright (c) Microsoft. All rights reserved.

// Package workflowprovider hosts a [workflow.Workflow] as an [agent.Agent].
//
// The first agent run on a session opens a streaming workflow run; subsequent
// runs reuse it. Workflow events are translated into agent
// [agent.ResponseUpdate]s as the workflow executes. Pending external
// requests raised by the workflow (via [workflow.RequestInfoEvent]) are
// surfaced as response updates carrying the request content; the caller can
// then resume by including the matching response content (e.g.
// [message.FunctionResultContent] or [message.ToolApprovalResponseContent])
// in the next agent run, and the provider routes them as
// [workflow.ExternalResponse]s.
package workflowprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var messagesSliceType = reflect.TypeFor[[]*message.Message]()

const workflowErrorMessage = "An error occurred while executing the workflow."

// Config configures a [workflow.Workflow] hosted as an [agent.Agent] via [New].
type Config struct {
	agent.Config

	// Environment is the execution environment used to run the workflow on
	// each agent turn. Defaults to [inproc.Default] when nil.
	Environment *inproc.ExecutionEnvironment

	// IncludeOutputsInResponse, if true, surfaces [workflow.OutputEvent]
	// payloads in the agent response stream when the payload is a
	// [*message.Message] or [[]*message.Message]. By default outputs are
	// observed only when they contain [*agent.ResponseUpdate] or [*agent.Response]
	// payloads emitted by hosted agents inside the workflow.
	IncludeOutputsInResponse bool

	// IncludeErrorDetails, if true, surfaces the full error message from
	// [workflow.ErrorEvent]s in the agent response stream. When false, a
	// generic message is emitted instead.
	IncludeErrorDetails bool
}

// New wraps a [*workflow.Workflow] as an [*agent.Agent].
//
// The workflow's start executor must accept [[]*message.Message] (typically
// configured via [messageworkflow.Configure]). On the first call to
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

	runFn := func(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			responseID := uuid.NewString()

			sess, _ := agent.GetOption(options, agent.WithSession)
			if sess != nil && sess.ServiceID() == "" {
				sess.SetServiceID(providerServiceID)
			}

			state, err := loadOrInitState(ctx, sess, env, wf)
			if err != nil {
				yield(nil, err)
				return
			}
			defer func() {
				_ = state.closeStream(ctx)
				saveState(sess, state)
			}()

			// Split incoming messages into ExternalResponses for pending
			// requests and the remaining workflow messages.
			remaining, responses, hasMatchedStartResponse, err := splitResponses(messages, state.pending, wf.StartExecutorID(), state.stream.ResponsePortExecutorID)
			if err != nil {
				yield(nil, err)
				return
			}

			if len(remaining) > 0 {
				if err := state.stream.SendMessage(ctx, remaining); err != nil {
					yield(nil, err)
					return
				}
			}
			for _, matched := range responses {
				if err := state.stream.SendResponse(ctx, matched.response); err != nil {
					yield(nil, err)
					return
				}
				delete(state.pending, matched.contentID)
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
				case workflow.SuperStepCompletedEvent:
					if e.CompletionInfo != nil && e.CompletionInfo.CheckpointInfo != nil {
						state.lastCheckpoint = e.CompletionInfo.CheckpointInfo
					}
					if !yield(newUpdate(responseID, e), nil) {
						return
					}
				case workflow.OutputEvent:
					switch out := e.Output.(type) {
					case *agent.ResponseUpdate:
						if !yield(stampUpdate(out, responseID, e), nil) {
							return
						}
					case *agent.Response:
						if out == nil {
							continue
						}
						for _, update := range out.ToUpdates() {
							if !yield(stampUpdate(update, responseID, e), nil) {
								return
							}
						}
					case *message.Message:
						if !cfg.IncludeOutputsInResponse {
							continue
						}
						if !yield(messageToUpdate(out, responseID, e), nil) {
							return
						}
					case []*message.Message:
						if !cfg.IncludeOutputsInResponse {
							continue
						}
						for _, msg := range out {
							if !yield(messageToUpdate(msg, responseID, e), nil) {
								return
							}
						}
					}
				case workflow.RequestInfoEvent:
					update, contentID, pending, ok, err := requestToUpdate(e.Request, responseID, e)
					if err != nil {
						update := newUpdate(responseID, e, workflowErrorContent(err, cfg.IncludeErrorDetails))
						if !yield(update, nil) {
							return
						}
						continue
					}
					if !ok {
						if !yield(newUpdate(responseID, e), nil) {
							return
						}
						continue
					}
					state.pending[contentID] = pending
					if !yield(update, nil) {
						return
					}
				case workflow.ErrorEvent:
					update := newUpdate(responseID, e, workflowErrorContent(e.Error, cfg.IncludeErrorDetails))
					if !yield(update, nil) {
						return
					}
				case workflow.ExecutorFailedEvent:
					update := newUpdate(responseID, e, workflowErrorContent(e.Error, cfg.IncludeErrorDetails))
					if !yield(update, nil) {
						return
					}
				default:
					if !yield(newUpdate(responseID, e), nil) {
						return
					}
				}
			}
		}
	}

	return agent.New(
		agent.ProviderConfig{
			ProviderName:  "workflow",
			Run:           runFn,
			CreateSession: createSession,
		},
		cfg.Config,
	), nil
}

func createSession(_ context.Context, sess *agent.Session, _ ...agent.Option) error {
	if sess != nil && sess.ServiceID() == "" {
		sess.SetServiceID(providerServiceID)
	}
	saveState(sess, newProviderState())
	return nil
}

type matchedExternalResponse struct {
	contentID string
	response  *workflow.ExternalResponse
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
	pending map[string]*workflow.ExternalRequest,
	startExecutorID string,
	lookupPortOwner func(portID string) (string, bool),
) ([]*message.Message, []matchedExternalResponse, bool, error) {
	if len(messages) == 0 || len(pending) == 0 {
		return messages, nil, false, nil
	}
	var (
		responses               []matchedExternalResponse
		hasMatchedStartResponse bool
		matchedContentIDs       map[string]struct{}
	)
	out := make([]*message.Message, 0, len(messages))
	for _, m := range messages {
		if m == nil {
			continue
		}
		var keep []message.Content
		for _, c := range m.Contents {
			if id, ok := responseContentID(c); ok {
				if _, matched := matchedContentIDs[id]; matched {
					continue
				}
				if request, found := pending[id]; found {
					if request == nil {
						return nil, nil, false, fmt.Errorf("workflowprovider: pending response %q has no external request", id)
					}
					responseData, err := normalizeResponseData(c, request)
					if err != nil {
						return nil, nil, false, err
					}
					response, err := request.CreateResponse(responseData)
					if err != nil {
						return nil, nil, false, err
					}
					responses = append(responses, matchedExternalResponse{
						contentID: id,
						response:  response,
					})
					if owner, ok := lookupPortOwner(request.PortInfo.PortID); ok && owner == startExecutorID {
						hasMatchedStartResponse = true
					}
					if matchedContentIDs == nil {
						matchedContentIDs = make(map[string]struct{})
					}
					matchedContentIDs[id] = struct{}{}
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
	return out, responses, hasMatchedStartResponse, nil
}

// responseContentID returns the matching key for a content item if it is a
// known response type.
func responseContentID(c message.Content) (string, bool) {
	switch v := c.(type) {
	case *message.FunctionResultContent:
		return v.CallID, true
	case *message.ToolApprovalResponseContent:
		return v.RequestID, true
	}
	return "", false
}

func normalizeResponseData(response message.Content, request *workflow.ExternalRequest) (any, error) {
	if request == nil {
		return nil, errors.New("workflowprovider: pending request has no external request")
	}
	originalRequest, _ := requestDataContent(request)
	switch r := response.(type) {
	case *message.FunctionResultContent:
		if req, ok := originalRequest.(*message.FunctionCallContent); ok {
			return cloneFunctionResultContent(r, req.CallID), nil
		}
		if !request.PortInfo.ResponseType.MatchPolymorphic(reflect.TypeFor[*message.FunctionResultContent]()) {
			if r.Result == nil {
				return nil, fmt.Errorf("workflowprovider: null result is not supported for request port %q with response type %v", request.PortInfo.PortID, request.PortInfo.ResponseType)
			}
			if request.PortInfo.ResponseType.MatchPolymorphic(reflect.TypeOf(r.Result)) || isPortableValueResult(r.Result) {
				return r.Result, nil
			}
			return nil, fmt.Errorf("workflowprovider: unexpected result type in FunctionResultContent %T; expecting %v", r.Result, request.PortInfo.ResponseType)
		}
	case *message.ToolApprovalResponseContent:
		if req, ok := originalRequest.(*message.ToolApprovalRequestContent); ok {
			return cloneToolApprovalResponseContent(r, req.RequestID), nil
		}
	}
	return response, nil
}

func isPortableValueResult(result any) bool {
	switch value := result.(type) {
	case workflow.PortableValue:
		return true
	case *workflow.PortableValue:
		return value != nil
	default:
		return false
	}
}

// requestToUpdate translates an [*workflow.ExternalRequest] into a
// [*agent.ResponseUpdate] that surfaces the request content to the caller.
// The exposed content ID is rewritten to the workflow-facing external request
// ID. The original request payload stays on the persisted ExternalRequest and
// is used to normalize the matching response before it is delivered back into
// the workflow.
func requestToUpdate(req *workflow.ExternalRequest, responseID string, raw any) (*agent.ResponseUpdate, string, *workflow.ExternalRequest, bool, error) {
	if req == nil {
		return nil, "", nil, false, nil
	}
	c, ok := requestDataContent(req)
	if !ok {
		surfaced, err := requestToFunctionCall(req)
		if err != nil {
			return nil, "", nil, false, fmt.Errorf("workflowprovider: failed to surface external request %q: %w", req.RequestID, err)
		}
		return newUpdate(responseID, raw, surfaced), surfaced.CallID, req, true, nil
	}
	surfaced, id, ok := requestContentForDelivery(req.RequestID, c)
	if !ok {
		return nil, "", nil, false, nil
	}
	return newUpdate(responseID, raw, surfaced), id, req, true, nil
}

func requestDataContent(req *workflow.ExternalRequest) (message.Content, bool) {
	if req == nil {
		return nil, false
	}
	if data, ok := req.Data.As(reflect.TypeFor[*message.FunctionCallContent]()); ok {
		return data.(*message.FunctionCallContent), true
	}
	if data, ok := req.Data.As(reflect.TypeFor[*message.ToolApprovalRequestContent]()); ok {
		return data.(*message.ToolApprovalRequestContent), true
	}
	content, ok := req.Data.Any().(message.Content)
	return content, ok
}

func cloneFunctionResultContent(content *message.FunctionResultContent, callID string) *message.FunctionResultContent {
	clone := *content
	clone.CallID = callID
	return &clone
}

func cloneToolApprovalResponseContent(content *message.ToolApprovalResponseContent, requestID string) *message.ToolApprovalResponseContent {
	clone := *content
	clone.RequestID = requestID
	return &clone
}

func workflowErrorContent(err error, includeDetails bool) *message.ErrorContent {
	text := workflowErrorMessage
	if includeDetails && err != nil {
		text = err.Error()
	}
	return &message.ErrorContent{Message: text}
}

func requestToFunctionCall(req *workflow.ExternalRequest) (*message.FunctionCallContent, error) {
	arguments, err := json.Marshal(map[string]any{"data": req.Data})
	if err != nil {
		return nil, err
	}
	return &message.FunctionCallContent{
		CallID:    req.RequestID,
		Name:      req.PortInfo.PortID,
		Arguments: string(arguments),
	}, nil
}

// requestContentForDelivery clones a request content item with the
// workflow-facing external request ID used as its response matching key.
func requestContentForDelivery(requestID string, c message.Content) (message.Content, string, bool) {
	switch v := c.(type) {
	case *message.FunctionCallContent:
		clone := *v
		clone.CallID = requestID
		return &clone, clone.CallID, true
	case *message.ToolApprovalRequestContent:
		clone := *v
		clone.RequestID = requestID
		return &clone, clone.RequestID, true
	}
	return nil, "", false
}

func messageToUpdate(m *message.Message, responseID string, raw any) *agent.ResponseUpdate {
	if m == nil {
		return newUpdate(responseID, raw)
	}
	return stampUpdate(&agent.ResponseUpdate{
		Role:       m.Role,
		Contents:   m.Contents,
		AuthorName: m.AuthorName,
		MessageID:  m.ID,
		CreatedAt:  m.CreatedAt,
	}, responseID, raw)
}

func newUpdate(responseID string, raw any, contents ...message.Content) *agent.ResponseUpdate {
	return stampUpdate(&agent.ResponseUpdate{
		Role:              message.RoleAssistant,
		Contents:          contents,
		RawRepresentation: raw,
	}, responseID, raw)
}

func stampUpdate(update *agent.ResponseUpdate, responseID string, raw any) *agent.ResponseUpdate {
	if update == nil {
		update = &agent.ResponseUpdate{}
	} else {
		clone := *update
		update = &clone
	}
	if update.Role == "" {
		update.Role = message.RoleAssistant
	}
	if update.MessageID == "" {
		update.MessageID = uuid.NewString()
	}
	if update.ResponseID == "" {
		update.ResponseID = responseID
	}
	if update.CreatedAt.IsZero() {
		update.CreatedAt = time.Now()
	}
	if update.RawRepresentation == nil {
		update.RawRepresentation = raw
	}
	return update
}
