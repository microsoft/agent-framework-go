// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"cmp"
	"maps"
	"slices"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

func toAgentMessage(in *a2a.Message) (*message.Message, error) {
	if in == nil {
		return nil, nil
	}

	contents, err := partsToContents(in.Parts, nil)
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

func responseToMessage(infoProvider a2a.TaskInfoProvider, resp *agent.Response) (*a2a.Message, error) {
	if resp == nil {
		return a2a.NewMessageForTask(a2a.MessageRoleAgent, infoProvider), nil
	}

	parts := make(a2a.ContentParts, 0)
	for _, msg := range resp.Messages {
		converted, err := contentsToParts(msg.Contents, nil)
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

func responseUpdateToMessage(infoProvider a2a.TaskInfoProvider, update *agent.ResponseUpdate) (*a2a.Message, error) {
	out := a2a.NewMessageForTask(a2a.MessageRoleAgent, infoProvider)
	if update == nil {
		return out, nil
	}

	parts, err := contentsToParts(update.Contents, nil)
	if err != nil {
		return nil, err
	}
	out.Parts = parts
	out.Metadata = maps.Clone(update.AdditionalProperties)
	out.ID = cmp.Or(update.MessageID, update.ResponseID, out.ID)
	return out, nil
}

func responseUpdateToWorkingStatusEvent(infoProvider a2a.TaskInfoProvider, update *agent.ResponseUpdate) (*a2a.TaskStatusUpdateEvent, error) {
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

func responseUpdateToArtifactEvent(infoProvider a2a.TaskInfoProvider, artifactID a2a.ArtifactID, update *agent.ResponseUpdate) (*a2a.TaskArtifactUpdateEvent, a2a.ArtifactID, error) {
	if update == nil {
		return nil, artifactID, nil
	}

	parts, err := contentsToParts(update.Contents, nil)
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

func responseToArtifactEvent(infoProvider a2a.TaskInfoProvider, resp *agent.Response) (*a2a.TaskArtifactUpdateEvent, error) {
	parts := make(a2a.ContentParts, 0)
	if resp != nil {
		for _, msg := range resp.Messages {
			converted, err := contentsToParts(msg.Contents, nil)
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
