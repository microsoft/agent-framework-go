// Copyright (c) Microsoft. All rights reserved.

package agentworkflow

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func TestWorkflowSessionStateJSON_WithPendingRequests(t *testing.T) {
	port := workflow.RequestPort{ID: "StringToInt", Request: reflect.TypeFor[string](), Response: reflect.TypeFor[int]()}
	first, err := workflow.NewExternalRequest("TestData", port, "Request1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := workflow.NewExternalRequest("OtherData", port, "Request2")
	if err != nil {
		t.Fatal(err)
	}
	value := providerState{
		workflowSessionID: "test-session-123",
		lastCheckpoint:    &workflow.CheckpointInfo{SessionID: "parent-session", CheckpointID: "parent-checkpoint"},
		pending:           map[string]*workflow.ExternalRequest{"call-1": first, "call-2": second},
	}
	data, err := json.Marshal(&value)
	if err != nil {
		t.Fatal(err)
	}
	var result providerState
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.workflowSessionID != value.workflowSessionID || result.lastCheckpoint == nil ||
		*result.lastCheckpoint != *value.lastCheckpoint || result.pending == nil || len(result.pending) != 2 {
		t.Fatalf("session state did not round trip: %#v", result)
	}
	for key, expected := range value.pending {
		actual := result.pending[key]
		if actual == nil || actual.RequestID != expected.RequestID || actual.PortInfo != expected.PortInfo {
			t.Fatalf("pending request %q = %#v, want %#v", key, actual, expected)
		}
		got, ok := workflow.PortableValueAs[string](actual.Data)
		want, _ := workflow.PortableValueAs[string](expected.Data)
		if !ok || got != want {
			t.Fatalf("request %q data = %q, %t, want %q", key, got, ok, want)
		}
	}
}

func TestWorkflowSessionStateJSON_WithoutPendingRequests(t *testing.T) {
	value := providerState{workflowSessionID: "test-session-456"}
	data, err := json.Marshal(&value)
	if err != nil {
		t.Fatal(err)
	}
	var result providerState
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.workflowSessionID != value.workflowSessionID || result.lastCheckpoint != nil || len(result.pending) != 0 {
		t.Fatalf("empty session state did not round trip: %#v", result)
	}
}
