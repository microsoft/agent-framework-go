// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
)

type Struct struct {
	Name  string `json:"name"`
	Age   int    `json:"age"`
	Email string `json:"email,omitempty"`
}

func TestForType(t *testing.T) {
	tests := []struct {
		name string
		v    any
	}{
		{"Struct", Struct{}},
		{"Struct", &Struct{}},
		{"map[string]int", map[string]int{}},
		{"[]string", []string{}},
		{"int", 0},
		{"string", ""},
		{"bool", true},
		{"bool", new(bool)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := jsonformat.ForType(reflect.TypeOf(tt.v))
			if err != nil {
				t.Fatal(err)
			}
			if f.Name != tt.name {
				t.Fatalf("expected: %v, got: %v", tt.name, f.Name)
			}
		})
	}
}

type Nested struct {
	Inner Struct   `json:"inner"`
	Items []Struct `json:"items,omitempty"`
}

// objectSchema returns the object schema reached by descending through path (a
// sequence of property names), stepping into array item schemas along the way.
func objectSchema(t *testing.T, format agent.ResponseFormat, path ...string) *jsonschema.Schema {
	t.Helper()
	s, ok := format.Schema.(*jsonschema.Schema)
	if !ok {
		t.Fatalf("schema has type %T, want *jsonschema.Schema", format.Schema)
	}
	for _, name := range path {
		next := s.Properties[name]
		if next == nil {
			t.Fatalf("property %q not found", name)
		}
		if next.Items != nil {
			next = next.Items
		}
		s = next
	}
	return s
}

// TestStrictRequiresAllProperties verifies that, for OpenAI strict structured
// outputs, every key in an object's "properties" also appears in "required",
// including omitempty fields. Otherwise OpenAI rejects the schema with HTTP 400.
func TestStrictRequiresAllProperties(t *testing.T) {
	format := jsonformat.MustFor[Nested]()
	if !format.Strict {
		t.Fatal("expected strict format")
	}
	check := func(name string, s *jsonschema.Schema) {
		props := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			props = append(props, k)
		}
		req := append([]string(nil), s.Required...)
		sort.Strings(props)
		sort.Strings(req)
		if len(props) != len(req) {
			t.Fatalf("%s: required %v does not cover all properties %v", name, req, props)
		}
		for i := range props {
			if props[i] != req[i] {
				t.Fatalf("%s: required %v does not cover all properties %v", name, req, props)
			}
		}
	}
	// Root object and the nested "inner" object and array element must all
	// require every property, including the omitempty "email" and "items".
	check("root", objectSchema(t, format))
	check("inner", objectSchema(t, format, "inner"))
	check("items element", objectSchema(t, format, "items"))
}

func TestFormatKind(t *testing.T) {
	format := jsonformat.MustFor[Struct]()
	if format.Kind != "json" {
		t.Fatalf("expected: %v, got: %v", "json", format.Kind)
	}
}

func TestNothing(t *testing.T) {
	format := jsonformat.Nothing()
	if format.Kind != "json" {
		t.Fatalf("expected: %v, got: %v", "json", format.Kind)
	}
}

func TestAny(t *testing.T) {
	format := jsonformat.Any()
	if format.Kind != "json" {
		t.Fatalf("expected: %v, got: %v", "json", format.Kind)
	}
}
