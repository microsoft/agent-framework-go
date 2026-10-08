// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"errors"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/microsoft/agent-framework-go/message"
)

// taskToContents flattens artifacts before any input-required status contents.
// A task with neither artifacts nor input requests has nil contents.
func taskToContents(task *a2a.Task) (message.Contents, error) {
	if task == nil {
		return nil, errors.New("a2aprovider: task cannot be nil")
	}
	var contents message.Contents
	for _, artifact := range task.Artifacts {
		if contents == nil {
			contents = make(message.Contents, 0)
		}
		var err error
		contents, err = partsToContents(artifact.Parts, contents)
		if err != nil {
			return nil, err
		}
	}
	input, err := statusInputContents(&task.Status)
	if err != nil {
		return nil, err
	}
	return append(contents, input...), nil
}

// taskToMessages keeps each artifact separate and appends an input-required
// status message last. It returns nil when the task has no output messages.
// Task-level metadata is left to the response layer; message metadata is cloned.
func taskToMessages(task *a2a.Task) ([]*message.Message, error) {
	if task == nil {
		return nil, errors.New("a2aprovider: task cannot be nil")
	}
	var messages []*message.Message
	for _, artifact := range task.Artifacts {
		msg, err := artifactToMessage(artifact)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	input, err := statusInputContents(&task.Status)
	if err != nil {
		return nil, err
	}
	if input != nil {
		messages = append(messages, &message.Message{
			Role:              message.RoleAssistant,
			ID:                task.Status.Message.ID,
			Contents:          input,
			RawRepresentation: task.Status,
		})
	}
	return messages, nil
}

// artifactToMessage preserves artifact identity, metadata and raw representation,
// including a non-nil empty content collection for an artifact with no parts.
func artifactToMessage(artifact *a2a.Artifact) (*message.Message, error) {
	if artifact == nil {
		return nil, errors.New("a2aprovider: artifact cannot be nil")
	}
	contents, err := partsToContents(artifact.Parts, make(message.Contents, 0))
	if err != nil {
		return nil, err
	}
	return &message.Message{
		Role:                 message.RoleAssistant,
		ID:                   string(artifact.ID),
		Contents:             contents,
		AdditionalProperties: cloneMetadata(artifact.Metadata),
		RawRepresentation:    artifact,
	}, nil
}

// statusInputContents returns nil unless an input-required message has contents.
// Converted contents retain their source parts and cloned part metadata.
func statusInputContents(status *a2a.TaskStatus) (message.Contents, error) {
	if status == nil {
		return nil, errors.New("a2aprovider: status cannot be nil")
	}
	if status.State != a2a.TaskStateInputRequired || status.Message == nil {
		return nil, nil
	}
	return partsToContents(status.Message.Parts, nil)
}
