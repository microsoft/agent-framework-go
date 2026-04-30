// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/internal/concurrent"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

type StepTracer interface {
	TraceActivated(executorID string)
	TraceCheckpointCreated(workflow.CheckpointInfo)
	TraceInstantiated(executorID string)
	TraceStatePublished()
}

// MessageEnvelope wraps a message with routing and tracing information.
type MessageEnvelope struct {
	Message      any
	SourceID     string
	TargetID     string
	TraceContext map[string]string

	declaredType workflow.TypeID
}

func NewMessageEnvelope(message any, declaredType reflect.Type, sourceID, targetID string) (*MessageEnvelope, error) {
	if declaredType == nil {
		declaredType = reflect.TypeOf(message)
	}
	if !reflect.TypeOf(message).AssignableTo(declaredType) {
		return nil, fmt.Errorf("the declared type %q is not compatible with the message instance of type %q", declaredType, reflect.TypeOf(message))
	}
	return &MessageEnvelope{
		Message:      message,
		declaredType: workflow.NewTypeID(declaredType),
		SourceID:     sourceID,
		TargetID:     targetID,
	}, nil
}

func NewMessageEnvelopeFromPortable(envelope *checkpoint.PortableMessageEnvelope) *MessageEnvelope {
	return &MessageEnvelope{
		Message:      envelope.Message.Any(),
		declaredType: envelope.MessageType,
		SourceID:     envelope.SourceID,
		TargetID:     envelope.TargetID,
		TraceContext: nil,
	}
}

func (e *MessageEnvelope) MessageType() workflow.TypeID {
	if e.declaredType == (workflow.TypeID{}) {
		return workflow.NewTypeID(reflect.TypeOf(e.Message))
	}
	return e.declaredType
}

func (e *MessageEnvelope) Portable() *checkpoint.PortableMessageEnvelope {
	return &checkpoint.PortableMessageEnvelope{
		MessageType: e.MessageType(),
		Message:     workflow.AnyPortableValue(e.Message),
		SourceID:    e.SourceID,
		TargetID:    e.TargetID,
	}
}

// IsExternal returns true if this message is from an external source.
func (e *MessageEnvelope) IsExternal() bool {
	return e.SourceID == ""
}

// StepContext manages the queued messages for a single workflow step.
// It provides thread-safe access to message queues for each executor.
type StepContext struct {
	queuedMessages concurrent.Map[string, *concurrent.Queue[*MessageEnvelope]]
}

// HasMessages returns true if there are any queued messages.
func (s *StepContext) HasMessages() bool {
	for _, value := range s.queuedMessages.All() {
		if !value.IsEmpty() {
			return true
		}
	}
	return false
}

func (s *StepContext) Keys() []string {
	var keys []string
	for key := range s.queuedMessages.All() {
		keys = append(keys, key)
	}
	return keys
}

// MessagesFor returns the messages queued for the given target executor.
// It initializes an empty slice if the target doesn't exist yet.
func (s *StepContext) MessagesFor(target string) *concurrent.Queue[*MessageEnvelope] {
	v, _ := s.queuedMessages.LoadOrStore(target, &concurrent.Queue[*MessageEnvelope]{})
	return v
}

// ExportMessages exports all queued messages for checkpointing.
func (s *StepContext) ExportMessages() map[string][]*checkpoint.PortableMessageEnvelope {
	result := make(map[string][]*checkpoint.PortableMessageEnvelope)
	for identity, envelopes := range s.queuedMessages.All() {
		exported := make([]*checkpoint.PortableMessageEnvelope, 0, envelopes.Len())
		for env := range envelopes.All() {
			exported = append(exported, env.Portable())
		}
		result[identity] = exported
	}
	return result
}

// ImportMessages imports queued messages from a checkpoint.
func (s *StepContext) ImportMessages(messages map[string][]*checkpoint.PortableMessageEnvelope) {
	for identity, envelopes := range messages {
		var imported concurrent.Queue[*MessageEnvelope]
		for _, env := range envelopes {
			imported.Enqueue(NewMessageEnvelopeFromPortable(env))
		}
		s.queuedMessages.Store(identity, &imported)
	}
}
