// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/workflow"
)

func TestValueRoundtrip(t *testing.T) {
	testRountrip(t, "string")
	testRountrip(t, 42)
	testRountrip(t, true)
	testRountrip(t, 3.14)
	testRountrip(t, agent.NewTextMessage("hello"))
	testRountrip(t, agent.RoleAssistant)
	testRountrip(t, agent.ErrorContent{Message: "error message"})
	testRountrip(t, workflow.AnyValue(0))
}

func testRountrip[T any](t *testing.T, v T) {
	t.Helper()
	testNonDelayedRountrip(t, v)
	testDelayedRoundtrip(t, v)
}

func testNonDelayedRountrip[T any](t *testing.T, v T) {
	t.Helper()
	pv := workflow.AnyValue(v)
	if _, ok := workflow.ValueAs[struct{}](pv); ok {
		t.Errorf("nondelayed: expected not to be struct{}")
	}
	got, ok := workflow.ValueAs[T](pv)
	if !ok {
		t.Errorf("nondelayed: expected to be able to convert to any")
	}
	if !reflect.DeepEqual(v, got) {
		t.Errorf("nondelayed: expected value %v, got %v", v, got)
	}
}

func testDelayedRoundtrip[T any](t *testing.T, v T) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Error(err)
	}
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Error(err)
	}
	pv := workflow.AnyValue(v)
	if _, ok := workflow.ValueAs[struct{}](pv); ok {
		t.Errorf("delayed: expected not to be struct{}")
	}
	got, ok := workflow.ValueAs[T](pv)
	if !ok {
		t.Errorf("delayed: expected to be able to convert to any")
	}
	if !reflect.DeepEqual(v, got) {
		t.Errorf("delayed: expected value %v, got %v", v, got)
	}
}
