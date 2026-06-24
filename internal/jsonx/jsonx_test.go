// Copyright (c) Microsoft. All rights reserved.

package jsonx

import (
	"encoding/json"
	"reflect"
	"testing"
)

type testUnion interface {
	testUnion()
}

type knownUnion struct {
	Type  string
	Value string
}

func (*knownUnion) testUnion() {}

type rawUnion struct {
	Raw json.RawMessage
}

func (*rawUnion) testUnion() {}

func TestUnmarshalDiscriminatedUnionSliceWithFallback_UsesFallbackForMissingAndUnknownTypes(t *testing.T) {
	data := []byte(`[{
		"Type":"known",
		"Value":"ok"
	},{
		"Type":"future",
		"Value":42
	},{
		"Value":"missing"
	}]`)
	types := map[string]reflect.Type{
		"known": reflect.TypeOf(knownUnion{}),
	}
	fallback := func(raw json.RawMessage) (testUnion, error) {
		return &rawUnion{Raw: append(json.RawMessage(nil), raw...)}, nil
	}

	values, err := UnmarshalDiscriminatedUnionSliceWithFallback(data, types, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(values))
	}
	known, ok := values[0].(*knownUnion)
	if !ok {
		t.Fatalf("values[0] = %T, want *knownUnion", values[0])
	}
	if known.Value != "ok" {
		t.Fatalf("known.Value = %q, want ok", known.Value)
	}
	if _, ok := values[1].(*rawUnion); !ok {
		t.Fatalf("values[1] = %T, want *rawUnion", values[1])
	}
	if _, ok := values[2].(*rawUnion); !ok {
		t.Fatalf("values[2] = %T, want *rawUnion", values[2])
	}
}

func TestUnmarshalDiscriminatedUnionSliceWithFallback_MissingTypeDoesNotMatchZeroValue(t *testing.T) {
	data := []byte(`[{
		"Type":"",
		"Value":"empty"
	},{
		"Value":"missing"
	}]`)
	types := map[string]reflect.Type{
		"": reflect.TypeOf(knownUnion{}),
	}
	fallback := func(raw json.RawMessage) (testUnion, error) {
		return &rawUnion{Raw: raw}, nil
	}

	values, err := UnmarshalDiscriminatedUnionSliceWithFallback(data, types, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := values[0].(*knownUnion); !ok {
		t.Fatalf("values[0] = %T, want *knownUnion", values[0])
	}
	if _, ok := values[1].(*rawUnion); !ok {
		t.Fatalf("values[1] = %T, want *rawUnion", values[1])
	}
}

func TestUnmarshalDiscriminatedUnionSlice_RejectsUnsupportedType(t *testing.T) {
	_, err := UnmarshalDiscriminatedUnionSlice[testUnion]([]byte(`[{"Type":"future"}]`), map[string]reflect.Type{})
	if err == nil {
		t.Fatal("expected error")
	}
}
