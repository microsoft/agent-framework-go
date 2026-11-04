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
	input, err := statusContents(&task.Status)
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
	status, err := statusToMessage(&task.Status)
	if err != nil {
		return nil, err
	}
	if status != nil {
		messages = append(messages, status)
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

// statusToMessage converts the optional status message, preserving its ID and
// metadata; the raw representation is the task status. It returns nil for routine progress and
// successful completion, or when the message has no contents.
func statusToMessage(status *a2a.TaskStatus) (*message.Message, error) {
	if status == nil {
		return nil, errors.New("a2aprovider: status cannot be nil")
	}
	if status.Message == nil {
		return nil, nil
	}
	switch status.State {
	case a2a.TaskStateSubmitted, a2a.TaskStateWorking, a2a.TaskStateCompleted:
		return nil, nil
	}
	msg, err := toAgentMessage(status.Message)
	if err != nil || len(msg.Contents) == 0 {
		return nil, err
	}
	if status.Message.Role != a2a.MessageRoleUser {
		msg.Role = message.RoleAssistant
	}
	msg.RawRepresentation = *status
	return msg, nil
}

// statusContents returns nil unless the status carries a message to surface.
// Converted contents retain their source parts and cloned part metadata.
func statusContents(status *a2a.TaskStatus) (message.Contents, error) {
	msg, err := statusToMessage(status)
	if msg == nil {
		return nil, err
	}
	return msg.Contents, nil
}
