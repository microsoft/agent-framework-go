// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"encoding/json"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
)

type person struct {
	Name string
}

type nullablePerson struct {
	Name string
}

func TestSessionState_Get_NonexistentKey_ReturnsNotFound(t *testing.T) {
	session := agenttest.CreateSession()
	var v string
	ok, err := session.Get("nonexistent", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected not found for nonexistent key")
	}
}

func TestSessionState_Set_And_Get_Roundtrips(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")

	var v string
	ok, err := session.Get("key1", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected key to be found")
	}
	if v != "value1" {
		t.Errorf("expected 'value1', got %v", v)
	}
}

func TestSessionState_Set_OverwritesExistingValue(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "original")
	session.Set("key1", "updated")

	var v string
	ok, err := session.Get("key1", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected key to be found")
	}
	if v != "updated" {
		t.Errorf("expected 'updated', got %v", v)
	}
}

func TestSessionState_Delete_ExistingKey(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")
	session.Delete("key1")

	var v string
	ok, err := session.Get("key1", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected key to be removed")
	}
}

func TestSessionState_Set_DifferentValueTypes(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("string", "hello")
	session.Set("int", 42)
	session.Set("bool", true)

	var str string
	if ok, err := session.Get("string", &str); err != nil || !ok || str != "hello" {
		t.Fatalf("string value mismatch: ok=%v err=%v value=%v", ok, err, str)
	}
	var num int
	if ok, err := session.Get("int", &num); err != nil || !ok || num != 42 {
		t.Fatalf("int value mismatch: ok=%v err=%v value=%v", ok, err, num)
	}
	var b bool
	if ok, err := session.Get("bool", &b); err != nil || !ok || !b {
		t.Fatalf("bool value mismatch: ok=%v err=%v value=%v", ok, err, b)
	}
}

func TestSessionState_Set_NilPointerValue_RoundtripsForSameType(t *testing.T) {
	session := agenttest.CreateSession()
	var p *nullablePerson
	session.Set("person", p)

	var out *nullablePerson
	ok, err := session.Get("person", &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected key to be found")
	}
	if out != nil {
		t.Fatalf("expected nil pointer, got %#v", out)
	}
}

func TestSessionState_Get_TypeMismatchReturnsError(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("count", 42)

	var s string
	ok, err := session.Get("count", &s)
	if ok {
		t.Fatal("expected type mismatch to report not found")
	}
	if err != nil {
		t.Fatalf("expected no error on type mismatch, got %v", err)
	}
}

func TestSessionState_Get_InvalidDestinationReturnsError(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("count", 42)

	var nonPtr int
	ok, err := session.Get("count", nonPtr)
	if ok {
		t.Fatal("expected unreadable destination to report not found")
	}
	if err == nil {
		t.Fatal("expected destination error")
	}
}

func TestSession_MarshalJSON_EmptyState(t *testing.T) {
	session := agenttest.CreateSession()
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var payload struct {
		State map[string]json.RawMessage
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if len(payload.State) != 0 {
		t.Errorf("expected marshaled session to contain empty State, got %q", string(data))
	}
}

func TestSession_MarshalJSON_DirectMarshalIncludesState(t *testing.T) {
	session := agenttest.CreateSession()
	session.Set("key1", "value1")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload struct {
		ServiceID string
		State     map[string]string
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if payload.ServiceID != "" {
		t.Fatalf("ServiceID = %q, want empty string", payload.ServiceID)
	}
	if len(payload.State) != 1 || payload.State["key1"] != "value1" {
		t.Fatalf("State = %#v, want map[string]string{\"key1\": \"value1\"}", payload.State)
	}
}

func TestSession_UnmarshalJSON_IntoCreatedSession(t *testing.T) {
	session := agenttest.CreateSession()
	err := json.Unmarshal([]byte(`{"State":{"key1":"value1"}}`), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var value string
	if ok, err := session.Get("key1", &value); err != nil || !ok || value != "value1" {
		t.Fatalf("session.Get(key1) = ok %v, value %q, err %v", ok, value, err)
	}
}

func TestSession_UnmarshalJSON_IntoZeroValueSession(t *testing.T) {
	var session agent.Session
	err := json.Unmarshal([]byte(`{"ServiceID":"service-123","State":{"key1":"value1"}}`), &session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := session.ServiceID(); got != "service-123" {
		t.Fatalf("session.ServiceID() = %q, want %q", got, "service-123")
	}

	var value string
	if ok, err := session.Get("key1", &value); err != nil || !ok || value != "value1" {
		t.Fatalf("session.Get(key1) = ok %v, value %q, err %v", ok, value, err)
	}
}

func TestSession_MarshalJSON_ZeroValueSession(t *testing.T) {
	var session agent.Session
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := string(data), `{"State":{},"ServiceID":""}`; got != want {
		t.Fatalf("json.Marshal(session) = %s, want %s", got, want)
	}
}

func TestSession_UnmarshalJSON_WithStateValues(t *testing.T) {
	session := agenttest.CreateSession()
	mustUnmarshalJSON(t, []byte(`{"State":{"key1":"value1","key2":42}}`), session)

	var s string
	if ok, err := session.Get("key1", &s); err != nil || !ok || s != "value1" {
		t.Fatalf("unexpected key1 result: ok=%v err=%v value=%q", ok, err, s)
	}
	var n float64
	if ok, err := session.Get("key2", &n); err != nil || !ok || n != 42 {
		t.Fatalf("unexpected key2 result: ok=%v err=%v value=%v", ok, err, n)
	}
}

func TestSessionState_Get_LazyDecodesAndCaches(t *testing.T) {
	session := agenttest.CreateSession()
	mustUnmarshalJSON(t, []byte(`{"State":{"person":{"Name":"Ada"}}}`), session)

	var person person
	ok, err := session.Get("person", &person)
	if !ok || err != nil {
		t.Fatalf("expected typed value, ok=%v err=%v", ok, err)
	}
	if person.Name != "Ada" {
		t.Fatalf("expected Ada, got %q", person.Name)
	}
}

func TestSessionState_Get_LazyDecodedValueTypeMismatchReturnsError(t *testing.T) {
	session := agenttest.CreateSession()
	mustUnmarshalJSON(t, []byte(`{"State":{"person":{"Name":"Ada"}}}`), session)

	var p person
	if ok, err := session.Get("person", &p); err != nil || !ok {
		t.Fatalf("expected successful decode, ok=%v err=%v", ok, err)
	}
	if p.Name != "Ada" {
		t.Fatalf("expected Ada, got %q", p.Name)
	}

	var n int
	ok, err := session.Get("person", &n)
	if ok {
		t.Fatal("expected type mismatch to report not found")
	}
	if err != nil {
		t.Fatalf("expected no error on type mismatch, got %v", err)
	}
}

func mustUnmarshalJSON(t *testing.T, data []byte, session *agent.Session) {
	t.Helper()
	if err := json.Unmarshal(data, session); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
}
