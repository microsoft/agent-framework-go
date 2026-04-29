// Copyright (c) Microsoft. All rights reserved.

package agent_test

import (
	"encoding/json"
	"strings"
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
	data, err := agenttest.New(nil).MarshalSession(t.Context(), session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(data), `"State":{}`) {
		t.Errorf("expected marshaled session to contain empty State, got %q", string(data))
	}
}

func TestSession_MarshalJSON_DirectMarshalReturnsError(t *testing.T) {
	session := agenttest.CreateSession()
	if _, err := json.Marshal(session); err == nil {
		t.Fatal("expected direct JSON marshaling to fail")
	}
}

func TestSession_UnmarshalJSON_DirectUnmarshalReturnsError(t *testing.T) {
	var session agent.Session
	if err := json.Unmarshal([]byte(`{"State":{"key1":"value1"}}`), &session); err == nil {
		t.Fatal("expected direct JSON unmarshaling to fail")
	}
}

func TestSession_UnmarshalJSON_IntoCreatedSessionReturnsGuardError(t *testing.T) {
	session := agenttest.CreateSession()
	err := json.Unmarshal([]byte(`{"State":{"key1":"value1"}}`), session)
	if err == nil {
		t.Fatal("expected direct JSON unmarshaling to fail")
	}
	if !strings.Contains(err.Error(), "sessions must be unmarshaled with Agent.UnmarshalSession") {
		t.Fatalf("expected guard error, got %v", err)
	}
}

func TestSession_UnmarshalSession_WithStateValues(t *testing.T) {
	session, err := agenttest.New(nil).UnmarshalSession(t.Context(), []byte(`{"State":{"key1":"value1","key2":42}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
	session, err := agenttest.New(nil).UnmarshalSession(t.Context(), []byte(`{"State":{"person":{"Name":"Ada"}}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
	session, err := agenttest.New(nil).UnmarshalSession(t.Context(), []byte(`{"State":{"person":{"Name":"Ada"}}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
