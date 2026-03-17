// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/microsoft/agent-framework-go/message"
)

func toAgentMessage(in *a2a.Message) (*message.Message, error) {
	if in == nil {
		return nil, nil
	}

	contents, err := partsToContents(in.Parts)
	if err != nil {
		return nil, err
	}

	role := message.RoleUser
	if in.Role == a2a.MessageRoleAgent {
		role = message.RoleAssistant
	}

	out := &message.Message{
		ID:                   in.ID,
		Role:                 role,
		Contents:             contents,
		AdditionalProperties: maps.Clone(in.Metadata),
		RawRepresentation:    in,
	}
	return out, nil
}

func partsToContents(parts []a2a.Part) (message.Contents, error) {
	contents := make(message.Contents, 0, len(parts))
	for _, part := range parts {
		switch p := part.(type) {
		case a2a.TextPart:
			contents = append(contents, &message.TextContent{
				ContentHeader: message.ContentHeader{AdditionalProperties: maps.Clone(p.Metadata), RawRepresentation: p},
				Text:          p.Text,
			})
		case a2a.FilePart:
			switch file := p.File.(type) {
			case a2a.FileURI:
				contents = append(contents, &message.URIContent{
					ContentHeader: message.ContentHeader{AdditionalProperties: maps.Clone(p.Metadata), RawRepresentation: p},
					URI:           file.URI,
					MediaType:     cmp.Or(file.MimeType, "application/octet-stream"),
				})
			case a2a.FileBytes:
				contents = append(contents, &message.DataContent{
					ContentHeader: message.ContentHeader{AdditionalProperties: maps.Clone(p.Metadata), RawRepresentation: p},
					Data:          file.Bytes,
					MediaType:     cmp.Or(file.MimeType, "application/octet-stream"),
				})
			default:
				return nil, fmt.Errorf("unsupported A2A file content type: %T", file)
			}
		case a2a.DataPart:
			dump, err := json.Marshal(p.Data)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal A2A data part: %w", err)
			}
			contents = append(contents, &message.DataContent{
				ContentHeader: message.ContentHeader{AdditionalProperties: maps.Clone(p.Metadata), RawRepresentation: p},
				Data:          base64.StdEncoding.EncodeToString(dump),
				MediaType:     "application/json",
			})
		default:
			return nil, fmt.Errorf("unsupported A2A part type: %T", part)
		}
	}
	return contents, nil
}

func responseToMessage(infoProvider a2a.TaskInfoProvider, resp *message.Response) (*a2a.Message, error) {
	if resp == nil {
		return a2a.NewMessageForTask(a2a.MessageRoleAgent, infoProvider), nil
	}

	parts := make([]a2a.Part, 0)
	for _, msg := range resp.Messages {
		converted, err := contentsToParts(msg.Contents)
		if err != nil {
			return nil, err
		}
		parts = append(parts, converted...)
	}

	out := a2a.NewMessageForTask(a2a.MessageRoleAgent, infoProvider, parts...)
	out.Metadata = maps.Clone(resp.AdditionalProperties)
	for _, msg := range slices.Backward(resp.Messages) {
		if msg != nil && msg.ID != "" {
			out.ID = msg.ID
			break
		}
	}
	return out, nil
}

func responseToArtifactEvent(infoProvider a2a.TaskInfoProvider, resp *message.Response) (*a2a.TaskArtifactUpdateEvent, error) {
	parts := make([]a2a.Part, 0)
	if resp != nil {
		for _, msg := range resp.Messages {
			converted, err := contentsToParts(msg.Contents)
			if err != nil {
				return nil, err
			}
			parts = append(parts, converted...)
		}
	}
	evt := a2a.NewArtifactEvent(infoProvider, parts...)
	if resp != nil {
		evt.Metadata = maps.Clone(resp.AdditionalProperties)
	}
	return evt, nil
}

func contentsToParts(contents message.Contents) ([]a2a.Part, error) {
	parts := make([]a2a.Part, 0, len(contents))
	for _, content := range contents {
		switch c := content.(type) {
		case *message.TextContent:
			if c.Text == "" {
				continue
			}
			parts = append(parts, a2a.TextPart{Text: c.Text, Metadata: maps.Clone(c.AdditionalProperties)})
		case *message.URIContent:
			parts = append(parts, a2a.FilePart{
				Metadata: maps.Clone(c.AdditionalProperties),
				File: a2a.FileURI{
					URI: c.URI,
					FileMeta: a2a.FileMeta{
						MimeType: c.MediaType,
					},
				},
			})
		case *message.DataContent:
			parts = append(parts, a2a.FilePart{
				Metadata: maps.Clone(c.AdditionalProperties),
				File: a2a.FileBytes{
					Bytes: c.Data,
					FileMeta: a2a.FileMeta{
						MimeType: c.MediaType,
						Name:     c.Name,
					},
				},
			})
			if c.MediaType == "application/json" {
				bytes, err := c.Bytes()
				if err != nil {
					return nil, err
				}
				var value map[string]any
				if err := json.Unmarshal(bytes, &value); err == nil {
					parts[len(parts)-1] = a2a.DataPart{Metadata: maps.Clone(c.AdditionalProperties), Data: value}
				}
			}
		case *message.HostedFileContent:
			parts = append(parts, a2a.FilePart{
				Metadata: maps.Clone(c.AdditionalProperties),
				File: a2a.FileURI{
					URI: c.FileID,
					FileMeta: a2a.FileMeta{
						MimeType: c.MediaType,
						Name:     c.Name,
					},
				},
			})
		case *message.FunctionCallContent, *message.FunctionResultContent:
			data, err := json.Marshal(c)
			if err != nil {
				return nil, err
			}
			parts = append(parts, a2a.TextPart{Text: string(data)})
		default:
			data, err := json.Marshal(c)
			if err != nil {
				return nil, fmt.Errorf("unsupported content type: %T", c)
			}
			parts = append(parts, a2a.TextPart{Text: string(data)})
		}
	}
	return parts, nil
}
