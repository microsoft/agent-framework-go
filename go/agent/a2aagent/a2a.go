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
	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
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

func NewAgent(client *a2aclient.Client, options *Options) *Agent {
	if client == nil {
		panic("client cannot be nil")
	}
	var opts Options
	if options != nil {
		opts = *options
		options = nil // prevent further use of the original options
	}
	return &Agent{
		Client:  client,
		Options: opts,
		iden:    agent.NewIdentity(opts.ID, opts.Name, opts.Description),
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

func (a *Agent) Run(ctx context.Context, options agent.RunOptions, messages ...*message.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		var thread *Thread
		if options.Thread == nil {
			thread = a.NewThread().(*Thread)
			options.Thread = thread
		} else if t, ok := options.Thread.(*Thread); ok {
			thread = t
		} else {
			yield(nil, errors.New("the provided thread is not compatible with the agent, only threads created by the agent can be used"))
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
			a.sendMsg(ctx, &options, params, yield)
		}
	}
}

func (a *Agent) sendMsg(ctx context.Context, options *agent.RunOptions, params *a2a.MessageSendParams, yield func(*agent.RunResponseUpdate, error) bool) {
	var seq iter.Seq2[a2a.Event, error]
	if options.Streaming.Or(false) {
		seq = a.Client.SendStreamingMessage(ctx, params)
	} else {
		resp, err := a.Client.SendMessage(ctx, params)
		seq = func(yield func(a2a.Event, error) bool) {
			yield(resp, err)
		}
	}
	id, name := a.iden.ID(), a.iden.Name()
	thread := options.Thread.(*Thread)
	for e, err := range seq {
		if err != nil {
			yield(nil, err)
			return
		}
		switch e := e.(type) {
		case *a2a.Task:
			if err := a.updateThreadContextID(thread, e.ContextID, string(e.ID)); err != nil {
				yield(nil, err)
				return
			}
			now := time.Now()
			for _, artifact := range e.Artifacts {
				contents, err := partsToContents(artifact.Parts)
				if err != nil {
					yield(nil, err)
					return
				}
				timestamp := now
				if e.Status.Timestamp != nil {
					timestamp = *e.Status.Timestamp
				}
				if !yield(&agent.RunResponseUpdate{
					RawRepresentation:    artifact,
					AdditionalProperties: artifact.Metadata,
					AgentID:              id,
					MessageID:            string(artifact.ID),
					ResponseID:           string(e.ID),
					Contents:             contents,
					Role:                 message.RoleAssistant,
					CreatedAt:            timestamp,
					AuthorName:           name,
				}, nil) {
					return
				}
			}
		case *a2a.Message:
			if err := a.updateThreadContextID(thread, e.ContextID, ""); err != nil {
				yield(nil, err)
				return
			}
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
				AgentID:              id,
				MessageID:            e.ID,
				ResponseID:           e.ID,
				Role:                 role,
				Contents:             contents,
				AuthorName:           name,
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
