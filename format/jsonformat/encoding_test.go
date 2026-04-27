// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
)

func TestEncodingRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		v    any
	}{
		{"Struct", Struct{Name: "Alice", Age: 30, Email: "alice@example.com"}},
		{"Struct", &Struct{Name: "Alice", Age: 30, Email: "alice@example.com"}},
		{"EmptyStruct", struct{}{}},
		{"map[string]int", map[string]int{"a": 1, "b": 2}},
		{"[]string", []string{"foo", "bar", "baz"}},
		{"int", 42},
		{"string", "hello, world"},
		{"bool", true},
		{"bool", new(bool)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := reflect.TypeOf(tt.v)
			format, err := jsonformat.ForType(rt)
			if err != nil {
				t.Fatal(err)
			}

			data, err := format.Marshal(tt.v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			v2 := reflect.New(rt).Interface()
			if err := format.Unmarshal(data, &v2); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got := reflect.ValueOf(v2).Elem().Interface()
			if !reflect.DeepEqual(tt.v, got) {
				t.Fatalf("expected: %+v, got: %+v", tt.v, got)
			}
		})
	}
}

func TestNormalizePreservesInterfaceValues(t *testing.T) {
	format := jsonformat.Any()
	var value any = map[string]any{"status": "ok", "value": 42}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	got, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", value)
	}
	if !reflect.DeepEqual(got, map[string]any{"status": "ok", "value": 42}) {
		t.Fatalf("expected interface value to be preserved, got %#v", got)
	}
}

func TestNormalizeAppliesDefaults(t *testing.T) {
	format := jsonformat.New("test", "", &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"count":  {Type: "integer"},
			"status": {Type: "string", Default: json.RawMessage(`"ok"`)},
		},
		Required:             []string{"count"},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	})
	value := map[string]any{"count": 1}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	if !reflect.DeepEqual(value, map[string]any{"count": 1, "status": "ok"}) {
		t.Fatalf("expected defaults to be applied, got %#v", value)
	}
}

func TestNormalizeStructValue(t *testing.T) {
	type output struct {
		Count int `json:"count"`
	}

	format := jsonformat.MustFor[output]()
	value := output{Count: 1}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
}

func TestNormalizeInterfaceStructValue(t *testing.T) {
	type output struct {
		Count int `json:"count"`
	}

	format := jsonformat.MustFor[output]()
	var value any = output{Count: 1}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
}

func TestNormalizeEmptyStruct(t *testing.T) {
	format := jsonformat.MustFor[struct{}]()
	value := struct{}{}

	if err := format.Normalize(&value); err != nil {
		t.Fatalf("Normalize: %v", err)
	}
}
