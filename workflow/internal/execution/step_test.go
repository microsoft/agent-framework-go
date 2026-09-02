// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

func TestMessageEnvelopeCheckpointPreservesDifferingDeclaredType(t *testing.T) {
	portable := &checkpoint.PortableMessageEnvelope{
		MessageType: workflow.NewTypeID(reflect.TypeFor[any]()),
		Message:     workflow.AnyPortableValue("message"),
		SourceID:    "source",
	}

	restored := NewMessageEnvelopeFromPortable(portable)
	if _, ok := restored.Message.(workflow.PortableValue); !ok {
		t.Fatalf("restored message type = %T, want workflow.PortableValue", restored.Message)
	}
	if concrete := restored.Message.(workflow.PortableValue).TypeID; concrete != workflow.NewTypeID(reflect.TypeFor[string]()) {
		t.Fatalf("restored concrete type ID = %v, want string", concrete)
	}
	if restored.MessageType() != portable.MessageType {
		t.Fatalf("restored message type ID = %v, want %v", restored.MessageType(), portable.MessageType)
	}
	roundTrip := restored.Portable()
	if roundTrip.Message.TypeID != workflow.NewTypeID(reflect.TypeFor[string]()) {
		t.Fatalf("stored concrete type ID = %v, want string", roundTrip.Message.TypeID)
	}
	if roundTrip.MessageType != portable.MessageType {
		t.Fatalf("stored declared type ID = %v, want %v", roundTrip.MessageType, portable.MessageType)
	}
}

func TestMessageEnvelopeCheckpointUnwrapsMatchingDeclaredType(t *testing.T) {
	stringType := workflow.NewTypeID(reflect.TypeFor[string]())
	restored := NewMessageEnvelopeFromPortable(&checkpoint.PortableMessageEnvelope{
		MessageType: stringType,
		Message:     workflow.AnyPortableValue("message"),
	})
	if message, ok := restored.Message.(string); !ok || message != "message" {
		t.Fatalf("restored message = %#v, want string message", restored.Message)
	}
}
