// Copyright (c) Microsoft. All rights reserved.

package a2aagent

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"
	"github.com/google/uuid"
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

	id string
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
	}
}

func (a *Agent) ID() string {
	if a.Options.ID != "" {
		return a.Options.ID
	}
	if a.id == "" {
		a.id = uuid.NewString()
	}
	return a.id
}

func (a *Agent) Name() string {
	return a.Options.Name
}

func (a *Agent) Description() string {
	return a.Options.Description
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

func (a *Agent) Run(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
	a2aMessage, err := toMessage(messages)
	if err != nil {
		return nil, err
	}
	var thread *Thread
	if t := ctx.GetThread(); t == nil {
		thread = a.NewThread().(*Thread)
	} else if t, ok := t.(*Thread); ok {
		thread = t
	} else {
		return nil, errors.New("the provided thread is not compatible with the agent, only threads created by the agent can be used")
	}
	a2aMessage.ContextID = thread.ContextID
	resp, err := a.Client.SendMessage(ctx.GetContext(), &a2a.MessageSendParams{Message: a2aMessage})
	if err != nil {
		return nil, err
	}
	switch e := resp.(type) {
	case *a2a.Message:
		if err := a.updateThreadContextID(thread, e.ContextID); err != nil {
			return nil, err
		}
		msg, err := fromMessage(e)
		if err != nil {
			return nil, err
		}
		return &agent.RunResponse{
			ID:                   e.ID,
			AgentID:              a.ID(),
			Messages:             []*message.Message{msg},
			RawRepresentation:    e,
			AdditionalProperties: e.Metadata,
		}, nil

	case *a2a.Task:
		if err := a.updateThreadContextID(thread, e.ContextID); err != nil {
			return nil, err
		}
		msgs, err := fromTask(e)
		if err != nil {
			return nil, err
		}
		return &agent.RunResponse{
			ID:                   string(e.ID),
			AgentID:              a.ID(),
			Messages:             msgs,
			RawRepresentation:    e,
			AdditionalProperties: e.Metadata,
		}, nil
	default:
		return nil, fmt.Errorf("unexpected A2A response type: %T", resp)
	}
}

func (a *Agent) RunStream(ctx *agent.RunContext, messages ...*message.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		a2aMessage, err := toMessage(messages)
		if err != nil {
			yield(nil, err)
			return
		}
		var thread *Thread
		if t := ctx.GetThread(); t == nil {
			thread = a.NewThread().(*Thread)
		} else if t, ok := t.(*Thread); ok {
			thread = t
		} else {
			yield(nil, errors.New("the provided thread is not compatible with the agent, only threads created by the agent can be used"))
			return
		}
		a2aMessage.ContextID = thread.ContextID
		for e, err := range a.Client.SendStreamingMessage(ctx.GetContext(), &a2a.MessageSendParams{Message: a2aMessage}) {
			if err != nil {
				yield(nil, err)
				return
			}
			e, ok := e.(*a2a.Message)
			if !ok {
				yield(nil, fmt.Errorf("unexpected A2A streaming response type: %T", e))
				return
			}
			if err := a.updateThreadContextID(thread, e.ContextID); err != nil {
				yield(nil, err)
				return
			}
			contents, err := partsToContents(e.Parts)
			if err != nil {
				yield(nil, err)
				return
			}
			yield(&agent.RunResponseUpdate{
				RawRepresentation:    e,
				AdditionalProperties: e.Metadata,
				AgentID:              a.ID(),
				MessageID:            e.ID,
				ResponseID:           e.ID,
				Role:                 message.RoleAssistant,
				Contents:             contents,
			}, nil)
		}
	}
}

func (a *Agent) updateThreadContextID(thread *Thread, id string) error {
	if thread == nil {
		return nil
	}
	// Surface cases where the A2A agent responds with a response that
	// has a different context ID than the thread's context ID.
	if thread.ContextID != "" && thread.ContextID != id {
		return fmt.Errorf("mismatched context ID: thread has %q but A2A response has %q", thread.ContextID, id)
	}
	thread.ContextID = id
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

func toMessage(messages []*message.Message) (*a2a.Message, error) {
	var parts []a2a.Part
	for _, msg := range messages {
		var err error
		parts, err = contentsToParts(msg.Contents, parts)
		if err != nil {
			return nil, err
		}
	}
	return &a2a.Message{
		ID:    uuid.NewString(),
		Role:  a2a.MessageRoleUser,
		Parts: parts,
	}, nil
}

func fromMessage(msg *a2a.Message) (*message.Message, error) {
	contents, err := partsToContents(msg.Parts)
	if err != nil {
		return nil, err
	}
	role := message.RoleUser
	if msg.Role == a2a.MessageRoleAgent {
		role = message.RoleAssistant
	}
	return &message.Message{
		ID:                msg.ID,
		Contents:          contents,
		Role:              role,
		RawRepresentation: msg,
	}, nil
}

func fromTask(task *a2a.Task) ([]*message.Message, error) {
	messages := make([]*message.Message, 0, len(task.Artifacts))
	for _, artifact := range task.Artifacts {
		msg, err := fromArtifact(artifact)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func fromArtifact(task *a2a.Artifact) (*message.Message, error) {
	contents, err := partsToContents(task.Parts)
	if err != nil {
		return nil, err
	}
	return &message.Message{
		AdditionalProperties: task.Metadata,
		Contents:             contents,
		Role:                 message.RoleAssistant,
		RawRepresentation:    task,
	}, nil
}
