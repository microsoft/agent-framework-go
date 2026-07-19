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
	"time"

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

func (o taskIDOpt) Value() any { return o.string }

// TaskID returns an [agent.Option] that associates the run with an existing A2A
// task, so the request continues that task rather than starting a new one.
func TaskID(taskID string) agent.Option {
	return taskIDOpt{taskID}
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
	setTaskIDs(session, slices.Collect(agent.AllOptions(options, TaskID)))
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
				sendMsg(session, a.subscribeToTaskWithFallback(ctx, a2a.TaskID(token)), yield)
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
			yieldTask(yield, task)
			return
		}
		var parts a2a.ContentParts
		for _, msg := range messages {
			parts = parts[:0] // reset parts slice
			parts, err := contentsToParts(msg.Contents, parts)
			if err != nil {
				yield(nil, err)
				return
			}
			params := &a2a.SendMessageRequest{Message: createA2AMessage(session, msg, parts)}
			var seq iter.Seq2[a2a.Event, error]
			if stream {
				seq = a.client.SendStreamingMessage(ctx, params)
			} else {
				resp, err := a.client.SendMessage(ctx, params)
				seq = func(yield func(a2a.Event, error) bool) {
					yield(resp, err)
				}
			}
			sendMsg(session, seq, yield)
		}
	}
}

func createA2AMessage(session *agent.Session, msg *message.Message, parts a2a.ContentParts) *a2a.Message {
	taskIDs := make([]a2a.TaskID, 0, 1)
	for _, taskID := range getTaskIDs(session) {
		taskIDs = append(taskIDs, a2a.TaskID(taskID))
	}

	a2aMessage := a2a.NewMessage(a2a.MessageRoleUser, parts...)
	if msg.ID != "" {
		a2aMessage.ID = msg.ID
	}
	a2aMessage.ContextID = getContextID(session)

	// When the task is waiting for user input (InputRequired), link the message
	// directly to the task via TaskID so it is treated as input for that task.
	// Otherwise, use ReferenceTasks to link as a follow-up.
	// See: https://github.com/a2aproject/A2A/blob/main/docs/topics/life-of-a-task.md#task-refinements
	if getLastTaskState(session) == a2a.TaskStateInputRequired && len(taskIDs) > 0 {
		a2aMessage.TaskID = taskIDs[len(taskIDs)-1]
	} else {
		a2aMessage.ReferenceTasks = taskIDs
	}
	a2aMessage.Metadata = maps.Clone(msg.AdditionalProperties)
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

func sendMsg(session *agent.Session, seq iter.Seq2[a2a.Event, error], yield func(*agent.ResponseUpdate, error) bool) {
	for e, err := range seq {
		if err != nil {
			yield(nil, err)
			return
		}
		taskInfo := e.TaskInfo()
		var taskState a2a.TaskState
		switch evt := e.(type) {
		case *a2a.Task:
			taskState = evt.Status.State
		case *a2a.TaskStatusUpdateEvent:
			taskState = evt.Status.State
		}
		if err := updateSessionContextID(session, taskInfo.ContextID, string(taskInfo.TaskID), taskState); err != nil {
			yield(nil, err)
			return
		}
		switch e := e.(type) {
		case *a2a.Task:
			if ok := yieldTask(yield, e); !ok {
				return
			}
		case *a2a.TaskStatusUpdateEvent:
			var (
				messageID string
				contents  []message.Content
			)
			if e.Status.Message != nil {
				messageID = e.Status.Message.ID
				if e.Status.State == a2a.TaskStateInputRequired || e.Status.State.Terminal() {
					var err error
					contents, err = partsToContents(e.Status.Message.Parts, nil)
					if err != nil {
						yield(nil, err)
						return
					}
				}
			}
			update := newResponseUpdate(e, e.Metadata, string(e.TaskID), messageID, message.RoleAssistant, contents, time.Now())
			if !yield(update, nil) {
				return
			}
		case *a2a.TaskArtifactUpdateEvent:
			contents, err := partsToContents(e.Artifact.Parts, nil)
			if err != nil {
				yield(nil, err)
				return
			}
			update := newResponseUpdate(e, e.Metadata, string(e.TaskID), string(e.Artifact.ID), message.RoleAssistant, contents, time.Now())
			if !yield(update, nil) {
				return
			}
		case *a2a.Message:
			contents, err := partsToContents(e.Parts, nil)
			if err != nil {
				yield(nil, err)
				return
			}
			role := message.RoleUser
			if e.Role == a2a.MessageRoleAgent {
				role = message.RoleAssistant
			}
			update := newResponseUpdate(e, e.Metadata, e.ID, e.ID, role, contents, time.Now())
			if !yield(update, nil) {
				return
			}
		default:
			yield(nil, fmt.Errorf("unsupported response type: %T", e))
			return
		}
	}
}

func newResponseUpdate(raw any, additionalProperties map[string]any, responseID, messageID string, role message.Role, contents message.Contents, createdAt time.Time) *agent.ResponseUpdate {
	return &agent.ResponseUpdate{
		RawRepresentation:    raw,
		AdditionalProperties: additionalProperties,
		ResponseID:           responseID,
		MessageID:            messageID,
		Role:                 role,
		Contents:             contents,
		CreatedAt:            createdAt,
	}
}

func yieldTask(yield func(*agent.ResponseUpdate, error) bool, task *a2a.Task) bool {
	now := time.Now()
	var continuationToken string
	switch task.Status.State {
	case a2a.TaskStateSubmitted, a2a.TaskStateWorking:
		continuationToken = string(task.ID)
	}
	timestamp := now
	if task.Status.Timestamp != nil {
		timestamp = *task.Status.Timestamp
	}
	var contents []message.Content
	messageID := ""
	if task.Status.Message != nil {
		messageID = task.Status.Message.ID
		// Mirror the streaming TaskStatusUpdateEvent path: surface the status
		// message text for states where it carries the agent's response (an
		// input-required follow-up question or a terminal summary), rather than
		// dropping it when the task has no artifacts.
		if task.Status.State == a2a.TaskStateInputRequired || task.Status.State.Terminal() {
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

	update := newResponseUpdate(task, task.Metadata, string(task.ID), messageID, message.RoleAssistant, contents, timestamp)
	update.ContinuationToken = continuationToken
	return yield(update, nil)
}

func updateSessionContextID(session *agent.Session, contextID, taskID string, taskState a2a.TaskState) error {
	if session == nil {
		return nil
	}
	// Surface cases where the A2A agent responds with a response that
	// has a different context ID than the session's context ID.
	currentContextID := getContextID(session)
	if currentContextID != "" && currentContextID != contextID {
		return fmt.Errorf("mismatched context ID: session has %q but A2A response has %q", currentContextID, contextID)
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
					AdditionalProperties: maps.Clone(part.Metadata),
					RawRepresentation:    part,
				},
				Text: string(c),
			}
		case a2a.URL:
			content = &message.URIContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: maps.Clone(part.Metadata),
					RawRepresentation:    part,
				},
				MediaType: cmp.Or(part.MediaType, "application/octet-stream"),
				URI:       string(c),
			}
		case a2a.Raw:
			content = &message.DataContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: maps.Clone(part.Metadata),
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
					AdditionalProperties: maps.Clone(part.Metadata),
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
			part.Metadata = maps.Clone(content.Header().AdditionalProperties)
			parts = append(parts, part)
		}
	}
	return parts, nil
}
