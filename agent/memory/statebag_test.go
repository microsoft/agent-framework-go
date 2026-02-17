// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"encoding/json"
	"testing"
)

func TestStateBag_Get_NonexistentKey_ReturnsNotFound(t *testing.T) {
	var bag StateBag
	_, ok := bag.Get("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent key")
	}
}

func TestStateBag_Set_And_Get_Roundtrips(t *testing.T) {
	var bag StateBag
	bag.Set("key1", "value1")
	v, ok := bag.Get("key1")
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
	v, ok := bag.Get("key1")
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
	_, ok := bag.Get("key1")
	if ok {
		t.Error("expected key to be removed")
	}
}

func TestStateBag_Delete_NonexistentKey(t *testing.T) {
	var bag StateBag
	bag.Delete("nonexistent")
}

func TestStateBag_Delete_DoesNotAffectOtherKeys(t *testing.T) {
	var bag StateBag
	bag.Set("key1", "value1")
	bag.Set("key2", "value2")
	bag.Delete("key1")
	_, ok := bag.Get("key1")
	if ok {
		t.Error("expected key1 to be removed")
	}
	v, ok := bag.Get("key2")
	if !ok {
		t.Fatal("expected key2 to still be present")
	}
	if v != "value2" {
		t.Errorf("expected 'value2', got %v", v)
	}
}

func TestStateBag_Set_DifferentValueTypes(t *testing.T) {
	var bag StateBag
	bag.Set("string", "hello")
	bag.Set("int", 42)
	bag.Set("bool", true)
	bag.Set("struct", struct{ Name string }{"test"})

	if v, ok := bag.Get("string"); !ok || v != "hello" {
		t.Errorf("string value mismatch")
	}
	if v, ok := bag.Get("int"); !ok || v != 42 {
		t.Errorf("int value mismatch")
	}
	if v, ok := bag.Get("bool"); !ok || v != true {
		t.Errorf("bool value mismatch")
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

func TestStateBag_MarshalJSON_WithValues(t *testing.T) {
	var bag StateBag
	bag.Set("key1", "value1")
	data, err := json.Marshal(&bag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to parse marshaled JSON: %v", err)
	}
	if m["key1"] != "value1" {
		t.Errorf("expected 'value1', got %v", m["key1"])
	}
}

func TestStateBag_UnmarshalJSON_Empty(t *testing.T) {
	var bag StateBag
	if err := json.Unmarshal([]byte("{}"), &bag); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, ok := bag.Get("nonexistent")
	if ok {
		t.Error("expected empty bag after unmarshaling empty object")
	}
}

func TestStateBag_UnmarshalJSON_WithValues(t *testing.T) {
	var bag StateBag
	if err := json.Unmarshal([]byte(`{"key1":"value1","key2":42}`), &bag); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := bag.Get("key1")
	if !ok {
		t.Fatal("expected key1 to be present")
	}
	// After JSON unmarshal, values are json.RawMessage
	var s string
	if err := json.Unmarshal(v.(json.RawMessage), &s); err != nil {
		t.Fatalf("failed to unmarshal key1 value: %v", err)
	}
	if s != "value1" {
		t.Errorf("expected 'value1', got %q", s)
	}
}

func TestStateBag_JSON_Roundtrip(t *testing.T) {
	var original StateBag
	original.Set("greeting", "hello")
	original.Set("count", 42)

	data, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var restored StateBag
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Verify greeting roundtrips
	v, ok := restored.Get("greeting")
	if !ok {
		t.Fatal("expected 'greeting' key after roundtrip")
	}
	var s string
	if err := json.Unmarshal(v.(json.RawMessage), &s); err != nil {
		t.Fatalf("failed to unmarshal greeting: %v", err)
	}
	if s != "hello" {
		t.Errorf("expected 'hello', got %q", s)
	}

	// Verify count roundtrips
	v, ok = restored.Get("count")
	if !ok {
		t.Fatal("expected 'count' key after roundtrip")
	}
	var n float64
	if err := json.Unmarshal(v.(json.RawMessage), &n); err != nil {
		t.Fatalf("failed to unmarshal count: %v", err)
	}
	if n != 42 {
		t.Errorf("expected 42, got %v", n)
	}
}
