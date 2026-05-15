// Copyright (c) Microsoft. All rights reserved.

package checkpoint_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

func TestPortableMessageEnvelope_JsonRoundtrip(t *testing.T) {
	envelope := checkpoint.PortableMessageEnvelope{
		MessageType: workflow.NewTypeID(reflect.TypeFor[string]()),
		Message:     workflow.AnyPortableValue("hello"),
		SourceID:    "source",
		TargetID:    "target",
		TraceContext: map[string]string{
			"traceparent": "00-00000000000000000000000000000001-0000000000000002-01",
		},
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got checkpoint.PortableMessageEnvelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.MessageType != envelope.MessageType {
		t.Fatalf("MessageType = %+v, want %+v", got.MessageType, envelope.MessageType)
	}
	if got.SourceID != envelope.SourceID {
		t.Fatalf("SourceID = %q, want %q", got.SourceID, envelope.SourceID)
	}
	if got.TargetID != envelope.TargetID {
		t.Fatalf("TargetID = %q, want %q", got.TargetID, envelope.TargetID)
	}
	if !reflect.DeepEqual(got.TraceContext, envelope.TraceContext) {
		t.Fatalf("TraceContext = %+v, want %+v", got.TraceContext, envelope.TraceContext)
	}
	message, ok := workflow.PortableValueAs[string](got.Message)
	if !ok || message != "hello" {
		t.Fatalf("Message = %q, %v; want hello, true", message, ok)
	}
}

func TestRunnerStateData_JsonRoundtrip(t *testing.T) {
	requestPort := workflow.RequestPort{
		ID:       "port",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	request, err := workflow.NewExternalRequest("request-1", requestPort, "question")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}
	state := checkpoint.RunnerStateData{
		InstantiatedExecutors: map[string]struct{}{
			"start": {},
			"next":  {},
		},
		QueuedMessages: map[string][]*checkpoint.PortableMessageEnvelope{
			"next": {
				{
					MessageType: workflow.NewTypeID(reflect.TypeFor[string]()),
					Message:     workflow.AnyPortableValue("queued"),
					SourceID:    "start",
					TargetID:    "next",
				},
			},
		},
		OutstandingRequests: []*workflow.ExternalRequest{request},
		RequestOwners: map[string]string{
			"request-1": "next",
		},
		ResponsePortOwners: map[string]string{
			"port": "next",
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got checkpoint.RunnerStateData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := got.InstantiatedExecutors["start"]; !ok || len(got.InstantiatedExecutors) != 2 {
		t.Fatalf("InstantiatedExecutors = %+v, want start and next", got.InstantiatedExecutors)
	}
	queued := got.QueuedMessages["next"]
	if len(queued) != 1 {
		t.Fatalf("QueuedMessages[next] count = %d, want 1", len(queued))
	}
	queuedMessage, ok := workflow.PortableValueAs[string](queued[0].Message)
	if !ok || queuedMessage != "queued" {
		t.Fatalf("QueuedMessages[next][0].Message = %q, %v; want queued, true", queuedMessage, ok)
	}
	if len(got.OutstandingRequests) != 1 || got.OutstandingRequests[0].RequestID != "request-1" {
		t.Fatalf("OutstandingRequests = %+v, want request-1", got.OutstandingRequests)
	}
	requestData, ok := workflow.PortableValueAs[string](got.OutstandingRequests[0].Data)
	if !ok || requestData != "question" {
		t.Fatalf("OutstandingRequests[0].Data = %q, %v; want question, true", requestData, ok)
	}
	if !reflect.DeepEqual(got.RequestOwners, state.RequestOwners) {
		t.Fatalf("RequestOwners = %+v, want %+v", got.RequestOwners, state.RequestOwners)
	}
	if !reflect.DeepEqual(got.ResponsePortOwners, state.ResponsePortOwners) {
		t.Fatalf("ResponsePortOwners = %+v, want %+v", got.ResponsePortOwners, state.ResponsePortOwners)
	}
}
