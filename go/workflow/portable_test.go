// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/workflow"
)

func TestPortableValueRoundtrip(t *testing.T) {
	testRountrip(t, "string")
	testRountrip(t, 42)
	testRountrip(t, true)
	testRountrip(t, 3.14)
	testRountrip(t, message.NewText("hello"))
	testRountrip(t, message.RoleAssistant)
	testRountrip(t, message.ErrorContent{Message: "error message"})
	testRountrip(t, workflow.AnyPortableValue(0))
}

func testRountrip[T any](t *testing.T, v T) {
	t.Helper()
	testNonDelayedRountrip(t, v)
	testDelayedRoundtrip(t, v)
}

func testNonDelayedRountrip[T any](t *testing.T, v T) {
	t.Helper()
	pv := workflow.AnyPortableValue(v)
	if _, ok := workflow.PortableValueAs[struct{}](pv); ok {
		t.Errorf("nondelayed: expected not to be struct{}")
	}
	got, ok := workflow.PortableValueAs[T](pv)
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
	pv := workflow.AnyPortableValue(v)
	if _, ok := workflow.PortableValueAs[struct{}](pv); ok {
		t.Errorf("delayed: expected not to be struct{}")
	}
	got, ok := workflow.PortableValueAs[T](pv)
	if !ok {
		t.Errorf("delayed: expected to be able to convert to any")
	}
	if !reflect.DeepEqual(v, got) {
		t.Errorf("delayed: expected value %v, got %v", v, got)
	}
}
