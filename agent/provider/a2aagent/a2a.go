// Copyright (c) Microsoft. All rights reserved.

package a2aagent

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
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
)

type Config struct {
	agent.Config
}

type taskIDOpt struct{ string }

func (o taskIDOpt) Value() any { return o.string }

func TaskID(taskID string) agentopt.Option {
	return taskIDOpt{taskID}
}

type a2aagent struct {
	client *a2aclient.Client
	cfg    Config
}

func New(aclient *a2aclient.Client, config Config) *agent.Agent {
	if aclient == nil {
		panic("a2aagent: client cannot be nil")
	}
	config.DisableFuncAutoCall = true // a2a doesn't support tool calls
	a := &a2aagent{
		client: aclient,
		cfg:    config,
	}
	return agent.New(agent.ProviderConfig{
		ProviderName:  "a2a",
		Run:           a.run,
		CreateSession: a.createSession,
	}, config.Config)
}

func (a *a2aagent) createSession(ctx context.Context, options ...agentopt.Option) (*memory.Session, error) {
	serviceID, _ := agentopt.Get(options, agentopt.ServiceID)
	session := memory.NewSession("")
	setContextID(session, serviceID)
	setTaskIDs(session, slices.Collect(agentopt.All(options, TaskID)))
	return session, nil
}

func (a *a2aagent) run(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		session, _ := agentopt.Get(options, agentopt.Session)
		stream, _ := agentopt.Get(options, agentopt.Stream)
		if token, ok := agentopt.Get(options, agentopt.ContinuationToken); ok && token != "" {
			if stream {
				// TODO: support resuming stream responses using continuation tokens.
				yield(nil, errors.New("reconnecting to task streams using continuation tokens is not supported yet"))
				return
			}
			task, err := a.client.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(token)})
			if err != nil {
				yield(nil, err)
				return
			}
			if err := updateSessionContextID(session, task.ContextID, string(task.ID)); err != nil {
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
			taskIDs := make([]a2a.TaskID, 0, 1)
			for _, taskID := range getTaskIDs(session) {
				taskIDs = append(taskIDs, a2a.TaskID(taskID))
			}
			userMsg := a2a.NewMessage(a2a.MessageRoleUser, parts...)
			if msg.ID != "" {
				userMsg.ID = msg.ID
			}
			userMsg.ContextID = getContextID(session)
			userMsg.ReferenceTasks = taskIDs
			userMsg.Metadata = maps.Clone(msg.AdditionalProperties)

			params := &a2a.SendMessageRequest{Message: userMsg}
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

func sendMsg(session *memory.Session, seq iter.Seq2[a2a.Event, error], yield func(*message.ResponseUpdate, error) bool) {
	for e, err := range seq {
		if err != nil {
			yield(nil, err)
			return
		}
		taskInfo := e.TaskInfo()
		if err := updateSessionContextID(session, taskInfo.ContextID, string(taskInfo.TaskID)); err != nil {
			yield(nil, err)
			return
		}
		switch e := e.(type) {
		case *a2a.Task:
			if ok := yieldTask(yield, e); !ok {
				return
			}
		case *a2a.TaskStatusUpdateEvent:
			if !yield(&message.ResponseUpdate{
				RawRepresentation:    e,
				AdditionalProperties: e.Metadata,
				MessageID:            string(e.TaskID),
				ResponseID:           string(e.TaskID),
				Role:                 message.RoleAssistant,
				CreatedAt:            time.Now(),
			}, nil) {
				return
			}
		case *a2a.TaskArtifactUpdateEvent:
			contents, err := partsToContents(e.Artifact.Parts, nil)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(&message.ResponseUpdate{
				RawRepresentation:    e,
				AdditionalProperties: e.Metadata,
				MessageID:            string(e.Artifact.ID),
				ResponseID:           string(e.TaskID),
				Contents:             contents,
				Role:                 message.RoleAssistant,
				CreatedAt:            time.Now(),
			}, nil) {
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
			if !yield(&message.ResponseUpdate{
				RawRepresentation:    e,
				AdditionalProperties: e.Metadata,
				MessageID:            e.ID,
				ResponseID:           e.ID,
				Role:                 role,
				Contents:             contents,
				CreatedAt:            time.Now(),
			}, nil) {
				return
			}
		default:
			yield(nil, fmt.Errorf("unsupported response type: %T", e))
			return
		}
	}
}

func yieldTask(yield func(*message.ResponseUpdate, error) bool, task *a2a.Task) bool {
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
	for _, artifact := range task.Artifacts {
		var err error
		contents, err = partsToContents(artifact.Parts, contents)
		if err != nil {
			yield(nil, err)
			return false
		}
	}

	if !yield(&message.ResponseUpdate{
		RawRepresentation:    task,
		AdditionalProperties: task.Metadata,
		ResponseID:           string(task.ID),
		Contents:             contents,
		ContinuationToken:    continuationToken,
		Role:                 message.RoleAssistant,
		CreatedAt:            timestamp,
	}, nil) {
		return false
	}
	return true
}

func updateSessionContextID(session *memory.Session, contextID, taskID string) error {
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
		default:
			return nil, fmt.Errorf("unsupported content type: %T", c)
		}
		if part != nil {
			part.Metadata = maps.Clone(content.Header().AdditionalProperties)
			parts = append(parts, part)
		}
	}
	return parts, nil
}
