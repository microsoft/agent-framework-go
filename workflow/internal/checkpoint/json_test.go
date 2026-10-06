// Copyright (c) Microsoft. All rights reserved.

package checkpoint_test

import (
	"encoding/json"
	"fmt"
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
	for _, tc := range []struct {
		name     string
		targetID string
	}{
		{name: "targeted", targetID: "next"},
		{name: "untargeted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
							TargetID:    tc.targetID,
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
			if !reflect.DeepEqual(got.InstantiatedExecutors, state.InstantiatedExecutors) {
				t.Fatalf("InstantiatedExecutors = %+v, want %+v", got.InstantiatedExecutors, state.InstantiatedExecutors)
			}
			if len(got.QueuedMessages) != len(state.QueuedMessages) {
				t.Fatalf("QueuedMessages count = %d, want %d", len(got.QueuedMessages), len(state.QueuedMessages))
			}
			queued, ok := got.QueuedMessages["next"]
			if !ok || len(queued) != 1 {
				t.Fatalf("QueuedMessages[next] = %+v, want one queued message", queued)
			}
			envelope := queued[0]
			if envelope == nil {
				t.Fatal("QueuedMessages[next][0] = nil, want a queued envelope")
			}
			wantEnvelope := state.QueuedMessages["next"][0]
			if envelope.MessageType != wantEnvelope.MessageType {
				t.Fatalf("queued MessageType = %+v, want %+v", envelope.MessageType, wantEnvelope.MessageType)
			}
			if envelope.SourceID != wantEnvelope.SourceID {
				t.Fatalf("queued SourceID = %q, want %q", envelope.SourceID, wantEnvelope.SourceID)
			}
			if envelope.TargetID != wantEnvelope.TargetID {
				t.Fatalf("queued TargetID = %q, want %q", envelope.TargetID, wantEnvelope.TargetID)
			}
			if envelope.Message.TypeID != wantEnvelope.Message.TypeID {
				t.Fatalf("queued Message.TypeID = %+v, want %+v", envelope.Message.TypeID, wantEnvelope.Message.TypeID)
			}
			queuedMessage, ok := workflow.PortableValueAs[string](envelope.Message)
			if !ok || queuedMessage != "queued" {
				t.Fatalf("QueuedMessages[next][0].Message = %q, %v; want queued, true", queuedMessage, ok)
			}
			if len(got.OutstandingRequests) != 1 || got.OutstandingRequests[0] == nil || got.OutstandingRequests[0].RequestID != "request-1" {
				t.Fatalf("OutstandingRequests = %+v, want request-1", got.OutstandingRequests)
			}
			restoredRequest := got.OutstandingRequests[0]
			if restoredRequest.PortInfo != request.PortInfo {
				t.Fatalf("OutstandingRequests[0].PortInfo = %+v, want %+v", restoredRequest.PortInfo, request.PortInfo)
			}
			if restoredRequest.Data.TypeID != request.Data.TypeID {
				t.Fatalf("OutstandingRequests[0].Data.TypeID = %+v, want %+v", restoredRequest.Data.TypeID, request.Data.TypeID)
			}
			requestData, ok := workflow.PortableValueAs[string](restoredRequest.Data)
			if !ok || requestData != "question" {
				t.Fatalf("OutstandingRequests[0].Data = %q, %v; want question, true", requestData, ok)
			}
			if !reflect.DeepEqual(got.RequestOwners, state.RequestOwners) {
				t.Fatalf("RequestOwners = %+v, want %+v", got.RequestOwners, state.RequestOwners)
			}
			if !reflect.DeepEqual(got.ResponsePortOwners, state.ResponsePortOwners) {
				t.Fatalf("ResponsePortOwners = %+v, want %+v", got.ResponsePortOwners, state.ResponsePortOwners)
			}
		})
	}
}

// A Checkpoint's StateData map is rebuilt from JSON on Unmarshal. The map must
// consistently apply its per-instance seed so Get can find keys inserted while
// restoring the checkpoint.
func TestCheckpoint_JsonRoundtrip_StateDataRemainsLoadable(t *testing.T) {
	key := workflow.ScopeKey{ID: workflow.ScopeID{ExecutorID: "exec1"}, Key: "k"}
	keyJSON, err := json.Marshal(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	valJSON, err := json.Marshal(workflow.AnyPortableValue("v"))
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	data := []byte(fmt.Sprintf(`{"StateData":[{"Key":%s,"Value":%s}]}`, keyJSON, valJSON))

	var cp checkpoint.Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := cp.StateData.Get(key); !ok {
		t.Fatal("restored StateData.Get(key) = false: the scope-key hasher is not deterministic across calls")
	}
}
