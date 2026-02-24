// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"encoding/json"
	"testing"
)

type stateBagPerson struct {
	Name string
}

func TestStateBag_Get_NonexistentKey_ReturnsNotFound(t *testing.T) {
	var bag StateBag
	var v string
	ok, err := bag.Get("nonexistent", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected not found for nonexistent key")
	}
}

func TestStateBag_Set_And_Get_Roundtrips(t *testing.T) {
	var bag StateBag
	bag.Set("key1", "value1")

	var v string
	ok, err := bag.Get("key1", &v)
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

func TestStateBag_Set_OverwritesExistingValue(t *testing.T) {
	var bag StateBag
	bag.Set("key1", "original")
	bag.Set("key1", "updated")

	var v string
	ok, err := bag.Get("key1", &v)
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

func TestStateBag_Delete_ExistingKey(t *testing.T) {
	var bag StateBag
	bag.Set("key1", "value1")
	bag.Delete("key1")

	var v string
	ok, err := bag.Get("key1", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected key to be removed")
	}
}

func TestStateBag_Set_DifferentValueTypes(t *testing.T) {
	var bag StateBag
	bag.Set("string", "hello")
	bag.Set("int", 42)
	bag.Set("bool", true)

	var str string
	if ok, err := bag.Get("string", &str); err != nil || !ok || str != "hello" {
		t.Fatalf("string value mismatch: ok=%v err=%v value=%v", ok, err, str)
	}
	var num int
	if ok, err := bag.Get("int", &num); err != nil || !ok || num != 42 {
		t.Fatalf("int value mismatch: ok=%v err=%v value=%v", ok, err, num)
	}
	var b bool
	if ok, err := bag.Get("bool", &b); err != nil || !ok || !b {
		t.Fatalf("bool value mismatch: ok=%v err=%v value=%v", ok, err, b)
	}
}

func TestStateBag_Get_TypeMismatchReturnsError(t *testing.T) {
	var bag StateBag
	bag.Set("count", 42)

	var s string
	ok, err := bag.Get("count", &s)
	if !ok {
		t.Fatal("expected key to exist")
	}
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
}

func TestStateBag_Get_InvalidDestinationReturnsError(t *testing.T) {
	var bag StateBag
	bag.Set("count", 42)

	var nonPtr int
	ok, err := bag.Get("count", nonPtr)
	if !ok {
		t.Fatal("expected key to exist")
	}
	if err == nil {
		t.Fatal("expected destination error")
	}
}

func TestStateBag_MarshalJSON_Empty(t *testing.T) {
	var bag StateBag
	data, err := json.Marshal(&bag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("expected '{}', got %q", string(data))
	}
}

func TestStateBag_UnmarshalJSON_WithValues(t *testing.T) {
	var bag StateBag
	if err := json.Unmarshal([]byte(`{"key1":"value1","key2":42}`), &bag); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var s string
	if ok, err := bag.Get("key1", &s); err != nil || !ok || s != "value1" {
		t.Fatalf("unexpected key1 result: ok=%v err=%v value=%q", ok, err, s)
	}
	var n float64
	if ok, err := bag.Get("key2", &n); err != nil || !ok || n != 42 {
		t.Fatalf("unexpected key2 result: ok=%v err=%v value=%v", ok, err, n)
	}
}

func TestStateBag_Get_LazyDecodesAndCaches(t *testing.T) {
	var bag StateBag
	if err := json.Unmarshal([]byte(`{"person":{"Name":"Ada"}}`), &bag); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var person stateBagPerson
	ok, err := bag.Get("person", &person)
	if !ok || err != nil {
		t.Fatalf("expected typed value, ok=%v err=%v", ok, err)
	}
	if person.Name != "Ada" {
		t.Fatalf("expected Ada, got %q", person.Name)
	}
}

func TestStateBagValue_TryReadDeserializedValue(t *testing.T) {
	v := newStateBagValueFromJSON(json.RawMessage(`{"Name":"Ada"}`))

	p, ok := tryReadDeserializedValue[stateBagPerson](v)
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
