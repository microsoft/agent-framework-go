// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func TestTypeID_JsonRoundtrip(t *testing.T) {
	cases := []reflect.Type{
		reflect.TypeFor[string](),
		reflect.TypeFor[int](),
		reflect.TypeFor[*workflow.Executor](),
		reflect.TypeFor[workflow.RequestPort](),
		reflect.TypeFor[map[string]int](),
	}
	for _, typ := range cases {
		t.Run(typ.String(), func(t *testing.T) {
			id := workflow.NewTypeID(typ)
			data, err := json.Marshal(id)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got workflow.TypeID
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got != id {
				t.Errorf("roundtrip = %+v, want %+v", got, id)
			}
			if !got.Match(typ) {
				t.Errorf("roundtripped TypeID does not match original type %v", typ)
			}
		})
	}
}

func TestTypeID_String(t *testing.T) {
	id := workflow.NewTypeID(reflect.TypeFor[workflow.RequestPort]())
	if got, want := id.String(), "RequestPort, github.com/microsoft/agent-framework-go/workflow"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestTypeID_NilType(t *testing.T) {
	if got := workflow.NewTypeID(nil); got != (workflow.TypeID{}) {
		t.Fatalf("NewTypeID(nil) = %+v, want zero TypeID", got)
	}
	if (workflow.TypeID{}).Match(nil) {
		t.Fatal("zero TypeID should not match nil type")
	}
	if (workflow.TypeID{}).Match(reflect.TypeFor[string]()) {
		t.Fatal("zero TypeID should not match concrete type")
	}
}

func TestEdgeConnection_JsonRoundtrip(t *testing.T) {
	cases := []workflow.EdgeConnection{
		{SourceIDs: []string{"a"}, SinkIDs: []string{"b"}},
		{SourceIDs: []string{"s1", "s2"}, SinkIDs: []string{"t1"}},
		{SourceIDs: []string{"src"}, SinkIDs: []string{"sink1", "sink2", "sink3"}},
	}
	for i, c := range cases {
		t.Run("case-"+itoa(i), func(t *testing.T) {
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got workflow.EdgeConnection
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !got.Equal(c) {
				t.Errorf("roundtrip = %+v, want %+v", got, c)
			}
		})
	}
}

func TestRequestPortInfo_JsonRoundtrip(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "MyPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	info := workflow.NewRequestPortInfo(port)
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got workflow.RequestPortInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.PortID != info.PortID {
		t.Errorf("ID = %q, want %q", got.PortID, info.PortID)
	}
	if got.RequestType != info.RequestType {
		t.Errorf("RequestType = %+v, want %+v", got.RequestType, info.RequestType)
	}
	if got.ResponseType != info.ResponseType {
		t.Errorf("ResponseType = %+v, want %+v", got.ResponseType, info.ResponseType)
	}
}

func TestExternalRequest_JsonRoundtrip(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "MyPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	request, err := workflow.NewExternalRequest("request-1", port, "payload")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got workflow.ExternalRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.RequestID != request.RequestID {
		t.Fatalf("ID = %q, want %q", got.RequestID, request.RequestID)
	}
	if got.PortInfo != request.PortInfo {
		t.Fatalf("PortInfo = %+v, want %+v", got.PortInfo, request.PortInfo)
	}
	value, ok := workflow.PortableValueAs[string](got.Data)
	if !ok || value != "payload" {
		t.Fatalf("Data = %q, %v; want payload, true", value, ok)
	}
}

func TestExternalResponse_JsonRoundtrip(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "MyPort",
		Request:  reflect.TypeFor[string](),
		Response: reflect.TypeFor[int](),
	}
	request, err := workflow.NewExternalRequest("request-1", port, "payload")
	if err != nil {
		t.Fatalf("NewExternalRequest: %v", err)
	}
	response, err := request.NewResponse(13)
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got workflow.ExternalResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.RequestID != response.RequestID {
		t.Fatalf("RequestID = %q, want %q", got.RequestID, response.RequestID)
	}
	if got.PortInfo != response.PortInfo {
		t.Fatalf("PortInfo = %+v, want %+v", got.PortInfo, response.PortInfo)
	}
	value, ok := workflow.PortableValueAs[int](got.Data)
	if !ok || value != 13 {
		t.Fatalf("Data = %d, %v; want 13, true", value, ok)
	}
}

func TestCheckpointInfo_JsonRoundtrip(t *testing.T) {
	info := workflow.NewCheckpointInfo("session-1")
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got workflow.CheckpointInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != info {
		t.Errorf("roundtrip = %+v, want %+v", got, info)
	}
}

func TestEdgeInfo_JsonRoundtrip(t *testing.T) {
	cases := []workflow.EdgeInfo{
		{
			Connection:   workflow.EdgeConnection{SourceIDs: []string{"a"}, SinkIDs: []string{"b"}},
			Label:        "",
			HasCondition: false,
			HasAssigner:  false,
		},
		{
			Connection:   workflow.EdgeConnection{SourceIDs: []string{"a"}, SinkIDs: []string{"b"}},
			Label:        "labelled",
			HasCondition: true,
			HasAssigner:  false,
		},
		{
			Connection:   workflow.EdgeConnection{SourceIDs: []string{"src"}, SinkIDs: []string{"t1", "t2"}},
			Label:        "",
			HasCondition: false,
			HasAssigner:  true,
		},
		{
			Connection:   workflow.EdgeConnection{SourceIDs: []string{"s1", "s2"}, SinkIDs: []string{"t"}},
			Label:        "fanin",
			HasCondition: false,
			HasAssigner:  false,
		},
	}
	for i, c := range cases {
		t.Run("case-"+itoa(i), func(t *testing.T) {
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got workflow.EdgeInfo
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !got.Connection.Equal(c.Connection) {
				t.Errorf("Connection mismatch: %+v vs %+v", got.Connection, c.Connection)
			}
			if got.Label != c.Label {
				t.Errorf("Label = %q, want %q", got.Label, c.Label)
			}
			if got.HasCondition != c.HasCondition {
				t.Errorf("HasCondition = %v, want %v", got.HasCondition, c.HasCondition)
			}
			if got.HasAssigner != c.HasAssigner {
				t.Errorf("HasAssigner = %v, want %v", got.HasAssigner, c.HasAssigner)
			}
		})
	}
}

func TestScopeID_JsonRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		id   workflow.ScopeID
	}{
		{
			name: "executor scope",
			id:   workflow.ScopeID{ExecutorID: "exec-1"},
		},
		{
			name: "named scope",
			id:   workflow.ScopeID{ScopeName: "shared-state"},
		},
		{
			name: "both fields",
			id:   workflow.ScopeID{ScopeName: "shared", ExecutorID: "exec-2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.id)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got workflow.ScopeID
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.ScopeName != tc.id.ScopeName {
				t.Errorf("ScopeName = %q, want %q", got.ScopeName, tc.id.ScopeName)
			}
			if got.ExecutorID != tc.id.ExecutorID {
				t.Errorf("ExecutorID = %q, want %q", got.ExecutorID, tc.id.ExecutorID)
			}
		})
	}
}

func TestScopeKey_JsonRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		key  workflow.ScopeKey
	}{
		{
			name: "executor scope key",
			key: workflow.ScopeKey{
				ID:  workflow.ScopeID{ExecutorID: "exec-1"},
				Key: "state-key",
			},
		},
		{
			name: "shared scope key",
			key: workflow.ScopeKey{
				ID:  workflow.ScopeID{ScopeName: "shared-state"},
				Key: "state-key",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.key)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got workflow.ScopeKey
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !got.Equal(tc.key) {
				t.Fatalf("roundtrip = %+v, want %+v", got, tc.key)
			}
		})
	}
}
