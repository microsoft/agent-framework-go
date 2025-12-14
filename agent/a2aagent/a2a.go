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
	"time"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
)

type Options struct {
	ID          string
	Name        string
	Description string
	DisplayName string
	Logger      *slog.Logger
}

var _ agent.Agent = (*Agent)(nil)

type Agent struct {
	Client  *a2aclient.Client
	Options Options

	iden agent.Identity
}

func NewAgent(client *a2aclient.Client, options Options) *Agent {
	if client == nil {
		panic("client cannot be nil")
	}
	return &Agent{
		Client:  client,
		Options: options,
		iden:    agent.NewIdentity(options.ID, options.Name, options.Description),
	}
}

func (a *Agent) Identity() agent.Identity {
	return a.iden
}

func (a *Agent) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		Streaming: true,
	}
}

func (a *Agent) NewThread() memory.Thread {
	return &Thread{}
}

func (a *Agent) NewThreadWithContextID(contextID string) *Thread {
	return &Thread{
		ContextID: contextID,
	}
}

func (a *Agent) UnmarshalThread(data []byte) (memory.Thread, error) {
	var thread Thread
	if err := json.Unmarshal(data, &thread); err != nil {
		return nil, err
	}
	return &thread, nil
}

func (a *Agent) Run(ctx context.Context, options ...agentopt.Option) iter.Seq2[*agent.RunResponseUpdate, error] {
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		var thread *Thread
		if v, ok := agentopt.Get(options, agentopt.Thread); !ok {
			// Aligning with other agent implementations that support background responses, where
			// a thread is required for background responses to prevent inconsistent experience
			// for callers if they forget to provide the thread for initial or follow-up runs.
			if opts, ok := agentopt.Get(options, agentopt.AllowBackgroundResponses); ok && opts {
				yield(nil, errors.New("a thread must be provided when AllowBackgroundResponses is enabled"))
				return
			}
			thread = a.NewThread().(*Thread)
		} else if t, ok := v.(*Thread); ok {
			thread = t
		} else {
			yield(nil, errors.New("the provided thread is not compatible with the agent, only threads created by the agent can be used"))
			return
		}
		streaming, _ := agentopt.Get(options, agentopt.Stream)
		if token, ok := agentopt.Get(options, agentopt.ContinuationToken); ok && token != nil {
			if streaming {
				// TODO: support resuming streaming responses using continuation tokens.
				yield(nil, errors.New("reconnecting to task streams using continuation tokens is not supported yet"))
				return
			}
			if _, ok := agentopt.Get(options, agentopt.Message); ok {
				yield(nil, errors.New("messages are not allowed when continuing a background response using a continuation token"))
				return
			}
			taskID, ok := token.(a2a.TaskID)
			if !ok {
				yield(nil, fmt.Errorf("invalid continuation token type: expected %T but got %T", a2a.TaskID(""), token))
				return
			}
			task, err := a.Client.GetTask(ctx, &a2a.TaskQueryParams{ID: taskID})
			if err != nil {
				yield(nil, err)
				return
			}
			if err := a.updateThreadContextID(thread, task.ContextID, string(task.ID)); err != nil {
				yield(nil, err)
				return
			}
			a.yieldTask(yield, task)
			return
		}
		var parts []a2a.Part
		for msg := range agentopt.All(options, agentopt.Message) {
			parts = parts[:0] // reset parts slice
			parts, err := contentsToParts(msg.Contents, parts)
			if err != nil {
				yield(nil, err)
				return
			}
			var taskIDs []a2a.TaskID
			if thread.TaskID != "" {
				taskIDs = append(taskIDs, a2a.TaskID(thread.TaskID))
			}
			params := &a2a.MessageSendParams{
				Message: &a2a.Message{
					ID:             msg.ID,
					Role:           a2a.MessageRoleUser,
					Parts:          parts,
					ReferenceTasks: taskIDs,
					ContextID:      thread.ContextID,
				},
			}
			a.sendMsg(ctx, thread, streaming, params, yield)
		}
	}
}

func (a *Agent) sendMsg(ctx context.Context, thread *Thread, streaming bool, params *a2a.MessageSendParams, yield func(*agent.RunResponseUpdate, error) bool) {
	var seq iter.Seq2[a2a.Event, error]
	if streaming {
		seq = a.Client.SendStreamingMessage(ctx, params)
	} else {
		resp, err := a.Client.SendMessage(ctx, params)
		seq = func(yield func(a2a.Event, error) bool) {
			yield(resp, err)
		}
	}
	for e, err := range seq {
		if err != nil {
			yield(nil, err)
			return
		}
		taskInfo := e.TaskInfo()
		if err := a.updateThreadContextID(thread, taskInfo.ContextID, string(taskInfo.TaskID)); err != nil {
			yield(nil, err)
			return
		}
		switch e := e.(type) {
		case *a2a.Task:
			if ok := a.yieldTask(yield, e); !ok {
				return
			}
		case *a2a.TaskStatusUpdateEvent:
			if !yield(&agent.RunResponseUpdate{
				RawRepresentation:    e,
				AdditionalProperties: e.Metadata,
				AgentID:              a.iden.ID(),
				MessageID:            string(e.TaskID),
				ResponseID:           string(e.TaskID),
				Role:                 message.RoleAssistant,
				AuthorName:           a.iden.Name(),
				CreatedAt:            time.Now(),
			}, nil) {
				return
			}
		case *a2a.TaskArtifactUpdateEvent:
			contents, err := partsToContents(e.Artifact.Parts)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(&agent.RunResponseUpdate{
				RawRepresentation:    e,
				AdditionalProperties: e.Metadata,
				AgentID:              a.iden.ID(),
				MessageID:            string(e.Artifact.ID),
				ResponseID:           string(e.TaskID),
				Contents:             contents,
				Role:                 message.RoleAssistant,
				AuthorName:           a.iden.Name(),
				CreatedAt:            time.Now(),
			}, nil) {
				return
			}
		case *a2a.Message:
			contents, err := partsToContents(e.Parts)
			if err != nil {
				yield(nil, err)
				return
			}
			role := message.RoleUser
			if e.Role == a2a.MessageRoleAgent {
				role = message.RoleAssistant
			}
			if !yield(&agent.RunResponseUpdate{
				RawRepresentation:    e,
				AdditionalProperties: e.Metadata,
				AgentID:              a.iden.ID(),
				MessageID:            e.ID,
				ResponseID:           e.ID,
				Role:                 role,
				Contents:             contents,
				AuthorName:           a.iden.Name(),
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

func (a *Agent) yieldTask(yield func(*agent.RunResponseUpdate, error) bool, task *a2a.Task) bool {
	now := time.Now()
	id, name := a.iden.ID(), a.iden.Name()
	for _, artifact := range task.Artifacts {
		contents, err := partsToContents(artifact.Parts)
		if err != nil {
			yield(nil, err)
			return false
		}
		timestamp := now
		if task.Status.Timestamp != nil {
			timestamp = *task.Status.Timestamp
		}
		var continuationToken any
		switch task.Status.State {
		case a2a.TaskStateSubmitted, a2a.TaskStateWorking:
			continuationToken = task.ID
		}
		if !yield(&agent.RunResponseUpdate{
			RawRepresentation:    artifact,
			AdditionalProperties: artifact.Metadata,
			AgentID:              id,
			MessageID:            string(artifact.ID),
			ResponseID:           string(task.ID),
			Contents:             contents,
			ContinuationToken:    continuationToken,
			Role:                 message.RoleAssistant,
			CreatedAt:            timestamp,
			AuthorName:           name,
		}, nil) {
			return false
		}
	}
	return true
}

func (a *Agent) updateThreadContextID(thread *Thread, contextID, taskID string) error {
	if thread == nil {
		return nil
	}
	// Surface cases where the A2A agent responds with a response that
	// has a different context ID than the thread's context ID.
	if thread.ContextID != "" && thread.ContextID != contextID {
		return fmt.Errorf("mismatched context ID: thread has %q but A2A response has %q", thread.ContextID, contextID)
	}
	thread.ContextID = contextID
	thread.TaskID = taskID
	return nil
}

func partsToContents(parts []a2a.Part) ([]message.Content, error) {
	contents := make([]message.Content, len(parts))
	for i, part := range parts {
		switch p := part.(type) {
		case a2a.TextPart:
			contents[i] = &message.TextContent{
				ContentHeader: message.ContentHeader{
					AdditionalProperties: p.Metadata,
					RawRepresentation:    p,
				},
				Text: p.Text,
			}
		case a2a.FilePart:
			switch f := p.File.(type) {
			case a2a.FileURI:
				contents[i] = &message.URIContent{
					ContentHeader: message.ContentHeader{
						AdditionalProperties: p.Metadata,
						RawRepresentation:    p,
					},
					MediaType: cmp.Or(f.MimeType, "application/octet-stream"),
					URI:       f.URI,
				}
			case a2a.FileBytes:
				contents[i] = &message.DataContent{
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
			contents[i] = &message.DataContent{
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
