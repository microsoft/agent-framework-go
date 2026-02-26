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
	"log/slog"
	"slices"
	"time"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/message"
)

type Options struct {
	ID          string
	Name        string
	Description string
	DisplayName string
	Logger      *slog.Logger
}

type taskIDOpt struct{ string }

func (taskIDOpt) CreateSessionOption() {}
func (o taskIDOpt) Value() any         { return o.string }

func TaskID(taskID string) agentopt.CreateSessionOption {
	return taskIDOpt{taskID}
}

type a2aagent struct {
	Client  *a2aclient.Client
	Options Options
}

func NewAgent(client *a2aclient.Client, options Options) *agent.Agent {
	if client == nil {
		panic("client cannot be nil")
	}
	a := &a2aagent{
		Client:  client,
		Options: options,
	}
	return agent.New(agent.Config{
		ID:               options.ID,
		Name:             options.Name,
		ProviderName:     "a2a",
		Description:      options.Description,
		CreateSession:    a.createSession,
		MarshalSession:   a.marshalSession,
		UnmarshalSession: a.unmarshalSession,
		Run:              a.run,
	})
}

func (a *a2aagent) createSession(ctx context.Context, options ...agentopt.CreateSessionOption) (*memory.Session, error) {
	serviceID, _ := agentopt.Get(options, agentopt.ServiceID)
	session := memory.NewSession("")
	setContextID(session, serviceID)
	setTaskIDs(session, slices.Collect(agentopt.All(options, TaskID)))
	return session, nil
}

func (a *a2aagent) marshalSession(_ context.Context, session *memory.Session) ([]byte, error) {
	if session == nil {
		return nil, errors.New("the provided session is nil")
	}
	return json.Marshal(session)
}

func (a *a2aagent) unmarshalSession(_ context.Context, data []byte) (*memory.Session, error) {
	var session memory.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (a *a2aagent) run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		session, _ := agentopt.Get(options, agentopt.Session)
		stream, _ := agentopt.Get(options, agentopt.Stream)
		if token, ok := agentopt.Get(options, agentopt.ContinuationToken); ok && token != "" {
			if stream {
				// TODO: support resuming stream responses using continuation tokens.
				yield(nil, errors.New("reconnecting to task streams using continuation tokens is not supported yet"))
				return
			}
			task, err := a.Client.GetTask(ctx, &a2a.TaskQueryParams{ID: a2a.TaskID(token)})
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
		var parts []a2a.Part
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
			params := &a2a.MessageSendParams{
				Message: &a2a.Message{
					ID:             msg.ID,
					Role:           a2a.MessageRoleUser,
					Parts:          parts,
					ReferenceTasks: taskIDs,
					ContextID:      getContextID(session),
				},
			}
			var seq iter.Seq2[a2a.Event, error]
			if stream {
				seq = a.Client.SendStreamingMessage(ctx, params)
			} else {
				resp, err := a.Client.SendMessage(ctx, params)
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

func partsToContents(parts []a2a.Part, contents []message.Content) ([]message.Content, error) {
	contents = slices.Grow(contents, len(parts))
	for _, part := range parts {
		var content message.Content
		switch p := part.(type) {
		case a2a.TextPart:
			content = &message.TextContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: p.Metadata,
					RawRepresentation:    p,
				},
				Text: p.Text,
			}
		case a2a.FilePart:
			switch f := p.File.(type) {
			case a2a.FileURI:
				content = &message.URIContent{
					ContentHeader: message.ContentHeader{
						AdditionalProperties: p.Metadata,
						RawRepresentation:    p,
					},
					MediaType: cmp.Or(f.MimeType, "application/octet-stream"),
					URI:       f.URI,
				}
			case a2a.FileBytes:
				content = &message.DataContent{
					ContentHeader: message.ContentHeader{
						AdditionalProperties: p.Metadata,
						RawRepresentation:    p,
					},
					MediaType: cmp.Or(f.MimeType, "application/octet-stream"),
					Data:      f.Bytes,
				}
			}
		case a2a.DataPart:
			dump, err := json.Marshal(p.Data)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal A2A data part: %w", err)
			}
			content = &message.DataContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: p.Metadata,
					RawRepresentation:    p,
				},
				Data:      base64.StdEncoding.EncodeToString(dump),
				MediaType: "application/json",
			}
		default:
			return nil, fmt.Errorf("unsupported A2A part type: %T", part)
		}
		if content != nil {
			contents = append(contents, content)
		}
	}
	return contents, nil
}

func contentsToParts(contents []message.Content, parts []a2a.Part) ([]a2a.Part, error) {
	for _, content := range contents {
		switch c := content.(type) {
		case *message.TextContent:
			parts = append(parts, a2a.TextPart{
				Metadata: c.AdditionalProperties,
				Text:     c.Text,
			})
		case *message.URIContent:
			parts = append(parts, a2a.FilePart{
				Metadata: c.AdditionalProperties,
				File: a2a.FileURI{
					URI: c.URI,
					FileMeta: a2a.FileMeta{
						MimeType: c.MediaType,
					},
				},
			})
		case *message.DataContent:
			parts = append(parts, a2a.FilePart{
				Metadata: c.AdditionalProperties,
				File: a2a.FileBytes{
					Bytes: c.Data,
					FileMeta: a2a.FileMeta{
						MimeType: c.MediaType,
					},
				},
			})
		case *message.HostedFileContent:
			parts = append(parts, a2a.FilePart{
				Metadata: c.AdditionalProperties,
				File: a2a.FileURI{
					URI: c.FileID,
					FileMeta: a2a.FileMeta{
						MimeType: c.MediaType,
						Name:     c.Name,
					},
				},
			})
		default:
			return nil, fmt.Errorf("unsupported content type: %T", c)
		}
	}
	return parts, nil
}
