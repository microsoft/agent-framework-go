// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

type person struct {
	Name string
}

func TestSessionState_Get_NonexistentKey_ReturnsNotFound(t *testing.T) {
	session := NewSession("")
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
	session := NewSession("")
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
	session := NewSession("")
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
	session := NewSession("")
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
	session := NewSession("")
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

func TestSessionState_Get_TypeMismatchReturnsError(t *testing.T) {
	session := NewSession("")
	session.Set("count", 42)

	var s string
	ok, err := session.Get("count", &s)
	if !ok {
		t.Fatal("expected key to exist")
	}
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
}

func TestSessionState_Get_InvalidDestinationReturnsError(t *testing.T) {
	session := NewSession("")
	session.Set("count", 42)

	var nonPtr int
	ok, err := session.Get("count", nonPtr)
	if !ok {
		t.Fatal("expected key to exist")
	}
	if err == nil {
		t.Fatal("expected destination error")
	}
}

func TestSession_MarshalJSON_EmptyState(t *testing.T) {
	session := NewSession("")
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(data), `"State":{}`) {
		t.Errorf("expected marshaled session to contain empty State, got %q", string(data))
	}
}

func TestSession_UnmarshalJSON_WithStateValues(t *testing.T) {
	var session Session
	if err := json.Unmarshal([]byte(`{"State":{"key1":"value1","key2":42}}`), &session); err != nil {
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
	var session Session
	if err := json.Unmarshal([]byte(`{"State":{"person":{"Name":"Ada"}}}`), &session); err != nil {
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

func TestStateValue_TryReadDeserializedValue(t *testing.T) {
	v := newStateValueFromJSON(json.RawMessage(`{"Name":"Ada"}`))

	p, ok := tryReadDeserializedValue[person](v)
	if !ok {
		t.Fatal("expected successful decode")
	}
	if p.Name != "Ada" {
		t.Fatalf("expected Ada, got %q", p.Name)
	}

	_, ok = tryReadDeserializedValue[int](v)
	if ok {
		t.Fatal("expected decode failure for incompatible type")
	}
}
