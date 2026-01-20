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

type contextIDOpt struct{ string }

func (contextIDOpt) NewThreadOption() {}
func (o contextIDOpt) Value() any     { return o.string }

func WithContextID(id string) agentopt.NewThreadOption {
	return contextIDOpt{id}
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

func (a *Agent) NewThread(ctx context.Context, options ...agentopt.NewThreadOption) memory.Thread {
	contextID, _ := agentopt.Get(options, WithContextID)
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

func (a *Agent) Run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		var thread *Thread
		if v, ok := agentopt.Get(options, agentopt.Thread); !ok {
			// Aligning with other agent implementations that support background responses, where
			// a thread is required for background responses to prevent inconsistent experience
			// for callers if they forget to provide the thread for initial or follow-up runs.
			if opts, ok := agentopt.Get(options, agentopt.AllowBackgroundResponses); ok && opts {
				yield(nil, errors.New("a thread must be provided when AllowBackgroundResponses is enabled"))
				return
			}
			thread = a.NewThread(ctx).(*Thread)
		} else if t, ok := v.(*Thread); ok {
			thread = t
		} else {
			yield(nil, errors.New("the provided thread is not compatible with the agent, only threads created by the agent can be used"))
			return
		}
		stream, _ := agentopt.Get(options, agentopt.Stream)
		if token, ok := agentopt.Get(options, agentopt.ContinuationToken); ok && token != "" {
			if stream {
				// TODO: support resuming stream responses using continuation tokens.
				yield(nil, errors.New("reconnecting to task streams using continuation tokens is not supported yet"))
				return
			}
			if len(messages) > 0 {
				yield(nil, errors.New("messages are not allowed when continuing a background response using a continuation token"))
				return
			}
			task, err := a.Client.GetTask(ctx, &a2a.TaskQueryParams{ID: a2a.TaskID(token)})
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
		for _, msg := range messages {
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
			a.sendMsg(ctx, thread, stream, params, yield)
		}
	}
}

func (a *Agent) sendMsg(ctx context.Context, thread *Thread, streaming bool, params *a2a.MessageSendParams, yield func(*message.ResponseUpdate, error) bool) {
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
			if !yield(&message.ResponseUpdate{
				RawRepresentation:    e,
				AdditionalProperties: e.Metadata,
				AuthorID:             a.iden.ID(),
				MessageID:            string(e.TaskID),
				ResponseID:           string(e.TaskID),
				Role:                 message.RoleAssistant,
				AuthorName:           a.iden.Name(),
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
				AuthorID:             a.iden.ID(),
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
				AuthorID:             a.iden.ID(),
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

func (a *Agent) yieldTask(yield func(*message.ResponseUpdate, error) bool, task *a2a.Task) bool {
	now := time.Now()
	id, name := a.iden.ID(), a.iden.Name()
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
		AuthorID:             id,
		ResponseID:           string(task.ID),
		Contents:             contents,
		ContinuationToken:    continuationToken,
		Role:                 message.RoleAssistant,
		CreatedAt:            timestamp,
		AuthorName:           name,
	}, nil) {
		return false
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
