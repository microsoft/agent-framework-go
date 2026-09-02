// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

// AgentConfig contains configuration for [NewAgent].
type AgentConfig struct {
	agent.Config
}

type taskIDOpt struct{ string }

func (o taskIDOpt) MAFValue() any { return o.string }

type metadataOpt struct{ value map[string]any }

func (o metadataOpt) MAFValue() any { return o.value }

// WithTaskID sets the existing A2A task to resume when creating a session.
// Supply it to [agent.Agent.CreateSession] together with a nonblank
// [agent.WithServiceID] context ID.
func WithTaskID(taskID string) agent.Option {
	return taskIDOpt{taskID}
}

// WithMetadata sets A2A request-level metadata for a run. When hosting an
// agent, the executor supplies incoming request metadata through this option.
func WithMetadata(metadata map[string]any) agent.Option {
	return metadataOpt{value: cloneMetadata(metadata)}
}

type a2aProvider struct {
	client *a2aclient.Client
	cfg    AgentConfig
}

// NewAgent creates a new [agent.Agent] that delegates runs to a remote agent
// over the A2A (Agent-to-Agent) protocol via the a2a client. It panics if
// aclient is nil.
func NewAgent(aclient *a2aclient.Client, config AgentConfig) *agent.Agent {
	if aclient == nil {
		panic("a2aprovider: client cannot be nil")
	}
	a := &a2aProvider{
		client: aclient,
		cfg:    config,
	}
	return agent.New(agent.ProviderConfig{
		ProviderName:  "a2a",
		Run:           a.run,
		CreateSession: a.createSession,
	}, config.Config)
}

func (a *a2aProvider) createSession(ctx context.Context, session *agent.Session, options ...agent.Option) error {
	contextID := session.ServiceID()
	if contextID != "" && strings.TrimSpace(contextID) == "" {
		return errors.New("a2aprovider: context ID cannot be blank")
	}
	taskID, hasTaskID := agent.GetOption(options, WithTaskID)
	if !hasTaskID {
		return nil
	}
	if strings.TrimSpace(contextID) == "" {
		return errors.New("a2aprovider: context ID is required with a task ID")
	}
	if strings.TrimSpace(taskID) == "" {
		return errors.New("a2aprovider: task ID cannot be blank")
	}
	setTaskID(session, taskID)
	return nil
}

func (a *a2aProvider) run(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		session, _ := agent.GetOption(options, agent.WithSession)
		stream, _ := agent.GetOption(options, agent.Stream)
		if token, ok := agent.GetOption(options, agent.WithContinuationToken); ok && token != "" {
			if len(messages) > 0 {
				yield(nil, errors.New("messages are not allowed when continuing a background response using a continuation token"))
				return
			}
			if stream {
				sendMsg(session, a.subscribeToTaskWithFallback(ctx, a2a.TaskID(token)), true, yield)
				return
			}
			task, err := a.client.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(token)})
			if err != nil {
				yield(nil, err)
				return
			}
			if err := updateSessionContextID(session, task.ContextID, string(task.ID), task.Status.State); err != nil {
				yield(nil, err)
				return
			}
			yieldTask(yield, task, true)
			return
		}
		if len(messages) == 0 {
			return
		}
		// Collect the parts of every input message into a single A2A message so the
		// run issues exactly one request, matching the framework's one-run/one-request
		// contract (and the .NET/Python A2A providers, which map a run's messages to a
		// single A2A Message). Issuing one request per message would also cross-link the
		// later messages to the task created by the first via ReferenceTasks/TaskID.
		var parts a2a.ContentParts
		var msgID string
		var metadata map[string]any
		for _, msg := range messages {
			var err error
			parts, err = contentsToParts(msg.Contents, parts)
			if err != nil {
				yield(nil, err)
				return
			}
			if msg.ID != "" {
				msgID = msg.ID
			}
			if len(msg.AdditionalProperties) > 0 {
				if metadata == nil {
					metadata = make(map[string]any, len(msg.AdditionalProperties))
				}
				maps.Copy(metadata, msg.AdditionalProperties)
			}
		}
		// Build a single combined message from the collected parts, ID and metadata,
		// then reuse createA2AMessage for the ContextID/task-linking logic so the run
		// issues exactly one request.
		combined := &message.Message{ID: msgID, AdditionalProperties: metadata}
		userMsg := createA2AMessage(session, combined, parts)

		params := &a2a.SendMessageRequest{Message: userMsg}
		if metadata, ok := agent.GetOption(options, WithMetadata); ok {
			params.Metadata = cloneMetadata(metadata)
		}
		var seq iter.Seq2[a2a.Event, error]
		if stream {
			seq = a.client.SendStreamingMessage(ctx, params)
		} else {
			if allowBackground, _ := agent.GetOption(options, agent.AllowBackgroundResponses); allowBackground {
				params.Config = &a2a.SendMessageConfig{ReturnImmediately: true}
			}
			resp, err := a.client.SendMessage(ctx, params)
			seq = func(yield func(a2a.Event, error) bool) {
				yield(resp, err)
			}
		}
		sendMsg(session, seq, stream, yield)
	}
}

func createA2AMessage(session *agent.Session, msg *message.Message, parts a2a.ContentParts) *a2a.Message {
	a2aMessage := a2a.NewMessage(a2a.MessageRoleUser, parts...)
	if msg.ID != "" {
		a2aMessage.ID = msg.ID
	}
	a2aMessage.ContextID = getContextID(session)
	taskID := getTaskID(session)

	// When the task is waiting for user input (InputRequired), link the message
	// directly to the task via TaskID so it is treated as input for that task.
	// Otherwise, use ReferenceTasks to link as a follow-up.
	// See: https://github.com/a2aproject/A2A/blob/main/docs/topics/life-of-a-task.md#task-refinements
	if taskID != "" {
		if getLastTaskState(session) == a2a.TaskStateInputRequired {
			a2aMessage.TaskID = a2a.TaskID(taskID)
		} else {
			a2aMessage.ReferenceTasks = []a2a.TaskID{a2a.TaskID(taskID)}
		}
	}
	a2aMessage.Metadata = cloneMetadata(msg.AdditionalProperties)
	return a2aMessage
}

// subscribeToTaskWithFallback resumes a task stream for a continuation token.
// It falls back to GetTask when SubscribeToTask returns a2a.ErrUnsupportedOperation,
// which can happen when the task has already reached a terminal state.
func (a *a2aProvider) subscribeToTaskWithFallback(ctx context.Context, taskID a2a.TaskID) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		for event, err := range a.client.SubscribeToTask(ctx, &a2a.SubscribeToTaskRequest{ID: taskID}) {
			if err == nil {
				if !yield(event, nil) {
					return
				}
				continue
			}

			if !errors.Is(err, a2a.ErrUnsupportedOperation) {
				yield(nil, err)
				return
			}

			task, getTaskErr := a.client.GetTask(ctx, &a2a.GetTaskRequest{ID: taskID})
			if getTaskErr != nil {
				yield(nil, getTaskErr)
				return
			}

			yield(task, nil)
			return
		}
	}
}

func sendMsg(session *agent.Session, seq iter.Seq2[a2a.Event, error], stream bool, yield func(*agent.ResponseUpdate, error) bool) {
	var contextID, taskID string
	var taskState a2a.TaskState
	for e, err := range seq {
		if err != nil {
			yield(nil, err)
			return
		}
		taskInfo := e.TaskInfo()
		if err := validateSessionContextID(session, taskInfo.ContextID); err != nil {
			yield(nil, err)
			return
		}
		if taskInfo.ContextID != "" {
			contextID = taskInfo.ContextID
		}
		switch evt := e.(type) {
		case *a2a.Task:
			taskID = string(evt.ID)
			taskState = evt.Status.State
		case *a2a.TaskStatusUpdateEvent:
			taskID = string(evt.TaskID)
			taskState = evt.Status.State
		case *a2a.TaskArtifactUpdateEvent:
			taskID = string(evt.TaskID)
		case *a2a.Message:
			taskID = ""
			taskState = a2a.TaskStateUnspecified
		}
		switch e := e.(type) {
		case *a2a.Task:
			if ok := yieldTask(yield, e, !stream); !ok {
				return
			}
		case *a2a.TaskStatusUpdateEvent:
			var (
				messageID string
				contents  []message.Content
			)
			if e.Status.Message != nil {
				messageID = e.Status.Message.ID
				if e.Status.State == a2a.TaskStateInputRequired {
					var err error
					contents, err = partsToContents(e.Status.Message.Parts, nil)
					if err != nil {
						yield(nil, err)
						return
					}
				}
			}
			update := newResponseUpdate(e, e.Metadata, string(e.TaskID), messageID, message.RoleAssistant, contents)
			update.FinishReason = finishReasonForTaskState(e.Status.State)
			if !yield(update, nil) {
				return
			}
		case *a2a.TaskArtifactUpdateEvent:
			contents, err := partsToContents(e.Artifact.Parts, nil)
			if err != nil {
				yield(nil, err)
				return
			}
			update := newResponseUpdate(e, cloneMetadata(e.Metadata), string(e.TaskID), string(e.Artifact.ID), message.RoleAssistant, contents)
			if !yield(update, nil) {
				return
			}
		case *a2a.Message:
			contents, err := partsToContents(e.Parts, nil)
			if err != nil {
				yield(nil, err)
				return
			}
			update := newResponseUpdate(e, e.Metadata, e.ID, e.ID, message.RoleAssistant, contents)
			update.FinishReason = "stop"
			if !yield(update, nil) {
				return
			}
		default:
			yield(nil, fmt.Errorf("unsupported response type: %T", e))
			return
		}
	}
	if err := updateSessionContextID(session, contextID, taskID, taskState); err != nil {
		yield(nil, err)
	}
}

func newResponseUpdate(raw any, additionalProperties map[string]any, responseID, messageID string, role message.Role, contents message.Contents) *agent.ResponseUpdate {
	return &agent.ResponseUpdate{
		RawRepresentation:    raw,
		AdditionalProperties: additionalProperties,
		ResponseID:           responseID,
		MessageID:            messageID,
		Role:                 role,
		Contents:             contents,
	}
}

// mergeMetadata combines a base metadata map with additional maps into a new
// map, cloning so the inputs are never mutated. Keys from later maps take
// precedence over earlier ones. It returns nil when every source is empty, so a
// task with no metadata yields no metadata map rather than an empty one.
func mergeMetadata(base map[string]any, extra ...map[string]any) map[string]any {
	var merged map[string]any
	if len(base) > 0 {
		merged = maps.Clone(base)
	}
	for _, m := range extra {
		if len(m) == 0 {
			continue
		}
		if merged == nil {
			merged = make(map[string]any, len(m))
		}
		maps.Copy(merged, m)
	}
	return merged
}

func yieldTask(yield func(*agent.ResponseUpdate, error) bool, task *a2a.Task, splitArtifacts bool) bool {
	var continuationToken string
	switch task.Status.State {
	case a2a.TaskStateSubmitted, a2a.TaskStateWorking:
		continuationToken = string(task.ID)
	}
	finishReason := finishReasonForTaskState(task.Status.State)
	if splitArtifacts {
		var yielded bool
		for _, artifact := range task.Artifacts {
			contents, err := partsToContents(artifact.Parts, nil)
			if err != nil {
				yield(nil, err)
				return false
			}
			update := newResponseUpdate(artifact, mergeMetadata(task.Metadata, artifact.Metadata), string(task.ID), string(artifact.ID), message.RoleAssistant, contents)
			update.ContinuationToken = continuationToken
			update.FinishReason = finishReason
			yielded = true
			if !yield(update, nil) {
				return false
			}
		}
		if task.Status.Message != nil && task.Status.State == a2a.TaskStateInputRequired {
			contents, err := partsToContents(task.Status.Message.Parts, nil)
			if err != nil {
				yield(nil, err)
				return false
			}
			update := newResponseUpdate(task.Status, nil, string(task.ID), task.Status.Message.ID, message.RoleAssistant, contents)
			update.ContinuationToken = continuationToken
			update.FinishReason = finishReason
			yielded = true
			if !yield(update, nil) {
				return false
			}
		}
		if yielded {
			return true
		}
		update := newResponseUpdate(task, cloneMetadata(task.Metadata), string(task.ID), "", "", nil)
		update.ContinuationToken = continuationToken
		update.FinishReason = finishReason
		return yield(update, nil)
	}

	var contents []message.Content
	messageID := ""
	if task.Status.Message != nil {
		messageID = task.Status.Message.ID
		// Mirror the streaming TaskStatusUpdateEvent path: surface the status
		// message when it carries an input-required follow-up question.
		if task.Status.State == a2a.TaskStateInputRequired {
			var err error
			contents, err = partsToContents(task.Status.Message.Parts, contents)
			if err != nil {
				yield(nil, err)
				return false
			}
		}
	}
	for _, artifact := range task.Artifacts {
		var err error
		contents, err = partsToContents(artifact.Parts, contents)
		if err != nil {
			yield(nil, err)
			return false
		}
	}
	update := newResponseUpdate(task, cloneMetadata(task.Metadata), string(task.ID), messageID, message.RoleAssistant, contents)
	update.ContinuationToken = continuationToken
	update.FinishReason = finishReason
	return yield(update, nil)
}

func finishReasonForTaskState(state a2a.TaskState) string {
	if state == a2a.TaskStateCompleted {
		return "stop"
	}
	return ""
}

func validateSessionContextID(session *agent.Session, contextID string) error {
	if session == nil {
		return nil
	}
	currentContextID := getContextID(session)
	if currentContextID != "" && contextID != "" && currentContextID != contextID {
		return fmt.Errorf("mismatched context ID: session has %q but A2A response has %q", currentContextID, contextID)
	}
	return nil
}

func updateSessionContextID(session *agent.Session, contextID, taskID string, taskState a2a.TaskState) error {
	if session == nil {
		return nil
	}
	// Surface cases where the A2A agent responds with a response that
	// has a different context ID than the session's context ID.
	if err := validateSessionContextID(session, contextID); err != nil {
		return err
	}
	setContextID(session, contextID)
	setTaskID(session, taskID)
	setLastTaskState(session, taskState)
	return nil
}

func partsToContents(parts a2a.ContentParts, contents []message.Content) ([]message.Content, error) {
	contents = slices.Grow(contents, len(parts))
	for _, part := range parts {
		if part == nil {
			continue
		}

		var content message.Content
		switch c := part.Content.(type) {
		case a2a.Text:
			content = &message.TextContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: cloneMetadata(part.Metadata),
					RawRepresentation:    part,
				},
				Text: string(c),
			}
		case a2a.URL:
			content = &message.URIContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: cloneMetadata(part.Metadata),
					RawRepresentation:    part,
				},
				MediaType: cmp.Or(part.MediaType, "application/octet-stream"),
				URI:       string(c),
			}
		case a2a.Raw:
			content = &message.DataContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: cloneMetadata(part.Metadata),
					RawRepresentation:    part,
				},
				Name:      part.Filename,
				MediaType: cmp.Or(part.MediaType, "application/octet-stream"),
				Data:      base64.StdEncoding.EncodeToString([]byte(c)),
			}
		case a2a.Data:
			dump, err := json.Marshal(c.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal A2A data part: %w", err)
			}
			content = &message.DataContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: cloneMetadata(part.Metadata),
					RawRepresentation:    part,
				},
				Name:      part.Filename,
				Data:      base64.StdEncoding.EncodeToString(dump),
				MediaType: cmp.Or(part.MediaType, "application/json"),
			}
		default:
			return nil, fmt.Errorf("unsupported A2A part type: %T", c)
		}

		contents = append(contents, content)
	}
	return contents, nil
}

func contentsToParts(contents []message.Content, parts a2a.ContentParts) (a2a.ContentParts, error) {
	for _, content := range contents {
		var part *a2a.Part
		switch c := content.(type) {
		case *message.TextContent:
			if c.Text == "" {
				continue
			}
			part = a2a.NewTextPart(c.Text)
		case *message.URIContent:
			part = a2a.NewFileURLPart(a2a.URL(c.URI), c.MediaType)
		case *message.DataContent:
			if c.MediaType == "application/json" {
				bytes, err := c.Bytes()
				if err != nil {
					return nil, err
				}
				var value any
				if err := json.Unmarshal(bytes, &value); err == nil {
					part = a2a.NewDataPart(value)
				}
			}
			if part == nil {
				bytes, err := c.Bytes()
				if err != nil {
					return nil, err
				}
				part = a2a.NewRawPart(bytes)
				part.MediaType = c.MediaType
			}
			part.Filename = c.Name
		case *message.HostedFileContent:
			part = a2a.NewFileURLPart(a2a.URL(c.FileID), c.MediaType)
			part.Filename = c.Name
		case *message.FunctionCallContent, *message.FunctionResultContent:
			data, err := json.Marshal(c)
			if err != nil {
				return nil, err
			}
			part = a2a.NewTextPart(string(data))
		default:
			data, err := json.Marshal(c)
			if err != nil {
				return nil, fmt.Errorf("unsupported content type: %T", c)
			}
			part = a2a.NewTextPart(string(data))
		}
		if part != nil {
			part.Metadata = cloneMetadata(content.Header().AdditionalProperties)
			parts = append(parts, part)
		}
	}
	return parts, nil
}
