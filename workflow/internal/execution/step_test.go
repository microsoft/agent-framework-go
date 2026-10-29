// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

type jsonPortSerializable struct {
	ID   int
	Name string
}

func TestPortableMessageEnvelopeJSON_BuiltInType(t *testing.T) {
	assertPortableEnvelopeJSON(t, "TestMessage")
}

func TestPortableMessageEnvelopeJSON_InternalType(t *testing.T) {
	assertPortableEnvelopeJSON(t, message.NewText("Hello, world!"))
}

func TestPortableMessageEnvelopeJSON_CustomType(t *testing.T) {
	assertPortableEnvelopeJSON(t, jsonPortSerializable{ID: 42, Name: "Test"})
}

func assertPortableEnvelopeJSON(t *testing.T, input any) {
	t.Helper()
	envelope, err := NewMessageEnvelope(input, reflect.TypeFor[any](), "Source1", "Target1")
	if err != nil {
		t.Fatal(err)
	}
	portable := envelope.Portable()
	data, err := json.Marshal(portable)
	if err != nil {
		t.Fatal(err)
	}
	var result checkpoint.PortableMessageEnvelope
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.MessageType != portable.MessageType || result.TargetID != portable.TargetID || result.Message.TypeID != portable.Message.TypeID {
		t.Fatalf("envelope metadata = %#v, want %#v", result, portable)
	}
	actual, ok := result.Message.As(reflect.TypeOf(input))
	if !ok || !reflect.DeepEqual(actual, input) {
		t.Fatalf("portable message = %#v, %t, want %#v", actual, ok, input)
	}
	reconstructed := NewMessageEnvelopeFromPortable(&result)
	if reconstructed.MessageType() != envelope.MessageType() || reconstructed.TargetID != envelope.TargetID {
		t.Fatalf("reconstructed envelope = %#v, want original declared type and target", reconstructed)
	}
	restored, ok := reconstructed.Message.(workflow.PortableValue)
	if !ok {
		t.Fatalf("reconstructed payload = %T, want PortableValue", reconstructed.Message)
	}
	actual, ok = restored.As(reflect.TypeOf(input))
	if !ok || !reflect.DeepEqual(actual, input) {
		t.Fatalf("reconstructed message = %#v, %t, want %#v", actual, ok, input)
	}
}

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
