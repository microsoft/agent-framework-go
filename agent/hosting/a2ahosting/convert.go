// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/a2aproject/a2a-go/v2/a2a"
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

func partsToContents(parts a2a.ContentParts) (message.Contents, error) {
	contents := make(message.Contents, 0, len(parts))
	for _, part := range parts {
		if part == nil {
			continue
		}
		switch c := part.Content.(type) {
		case a2a.Text:
			contents = append(contents, &message.TextContent{
				ContentHeader: message.ContentHeader{AdditionalProperties: maps.Clone(part.Metadata), RawRepresentation: part},
				Text:          string(c),
			})
		case a2a.URL:
			contents = append(contents, &message.URIContent{
				ContentHeader: message.ContentHeader{AdditionalProperties: maps.Clone(part.Metadata), RawRepresentation: part},
				URI:           string(c),
				MediaType:     cmp.Or(part.MediaType, "application/octet-stream"),
			})
		case a2a.Raw:
			contents = append(contents, &message.DataContent{
				ContentHeader: message.ContentHeader{AdditionalProperties: maps.Clone(part.Metadata), RawRepresentation: part},
				Name:          part.Filename,
				Data:          base64.StdEncoding.EncodeToString([]byte(c)),
				MediaType:     cmp.Or(part.MediaType, "application/octet-stream"),
			})
		case a2a.Data:
			dump, err := json.Marshal(c.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal A2A data part: %w", err)
			}
			contents = append(contents, &message.DataContent{
				ContentHeader: message.ContentHeader{AdditionalProperties: maps.Clone(part.Metadata), RawRepresentation: part},
				Name:          part.Filename,
				Data:          base64.StdEncoding.EncodeToString(dump),
				MediaType:     cmp.Or(part.MediaType, "application/json"),
			})
		default:
			return nil, fmt.Errorf("unsupported A2A part type: %T", c)
		}
	}
	return contents, nil
}

func responseToMessage(infoProvider a2a.TaskInfoProvider, resp *message.Response) (*a2a.Message, error) {
	if resp == nil {
		return a2a.NewMessageForTask(a2a.MessageRoleAgent, infoProvider), nil
	}

	parts := make(a2a.ContentParts, 0)
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

func responseUpdateToMessage(infoProvider a2a.TaskInfoProvider, update *message.ResponseUpdate) (*a2a.Message, error) {
	out := a2a.NewMessageForTask(a2a.MessageRoleAgent, infoProvider)
	if update == nil {
		return out, nil
	}

	parts, err := contentsToParts(update.Contents)
	if err != nil {
		return nil, err
	}
	out.Parts = parts
	out.Metadata = maps.Clone(update.AdditionalProperties)
	out.ID = cmp.Or(update.MessageID, update.ResponseID, out.ID)
	return out, nil
}

func responseUpdateToWorkingStatusEvent(infoProvider a2a.TaskInfoProvider, update *message.ResponseUpdate) (*a2a.TaskStatusUpdateEvent, error) {
	var progressMessage *a2a.Message
	var err error
	if update != nil && len(update.Contents) > 0 {
		progressMessage, err = responseUpdateToMessage(infoProvider, update)
		if err != nil {
			return nil, err
		}
	}

	working := a2a.NewStatusUpdateEvent(infoProvider, a2a.TaskStateWorking, progressMessage)
	if update != nil {
		working.Metadata = maps.Clone(update.AdditionalProperties)
		if update.ContinuationToken != "" {
			if working.Metadata == nil {
				working.Metadata = map[string]any{}
			}
			working.Metadata[continuationTokenMetadataKey] = update.ContinuationToken
		}
	}
	return working, nil
}

func responseUpdateToArtifactEvent(infoProvider a2a.TaskInfoProvider, artifactID a2a.ArtifactID, update *message.ResponseUpdate) (*a2a.TaskArtifactUpdateEvent, a2a.ArtifactID, error) {
	if update == nil {
		return nil, artifactID, nil
	}

	parts, err := contentsToParts(update.Contents)
	if err != nil {
		return nil, artifactID, err
	}
	if len(parts) == 0 {
		return nil, artifactID, nil
	}

	nextArtifactID := artifactID
	stableID := cmp.Or(update.ResponseID, update.MessageID)
	if stableID != "" {
		nextArtifactID = a2a.ArtifactID(stableID)
	}
	if nextArtifactID == "" {
		nextArtifactID = a2a.NewArtifactID()
	}

	evt := a2a.NewArtifactEvent(infoProvider, parts...)
	evt.Artifact.ID = nextArtifactID
	evt.Append = artifactID != "" && artifactID == nextArtifactID
	evt.LastChunk = false
	evt.Metadata = maps.Clone(update.AdditionalProperties)
	return evt, nextArtifactID, nil
}

func responseToArtifactEvent(infoProvider a2a.TaskInfoProvider, resp *message.Response) (*a2a.TaskArtifactUpdateEvent, error) {
	parts := make(a2a.ContentParts, 0)
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
	evt.LastChunk = true
	if resp != nil {
		evt.Metadata = maps.Clone(resp.AdditionalProperties)
	}
	return evt, nil
}

func contentsToParts(contents message.Contents) (a2a.ContentParts, error) {
	parts := make(a2a.ContentParts, 0, len(contents))
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
