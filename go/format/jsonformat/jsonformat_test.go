// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework/go/format/jsonformat"
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
			if f.Name() != tt.name {
				t.Fatalf("expected: %v, got: %v", tt.name, f.Name())
			}
		})
	}
}

func TestFormatKind(t *testing.T) {
	format := jsonformat.MustFor[Struct]()
	if format.Kind() != "json" {
		t.Fatalf("expected: %v, got: %v", "json", format.Kind())
	}
}

func TestNothing(t *testing.T) {
	format := jsonformat.Nothing()
	if format.Kind() != "json" {
		t.Fatalf("expected: %v, got: %v", "json", format.Kind())
	}
}

func TestAny(t *testing.T) {
	format := jsonformat.Any()
	if format.Kind() != "json" {
		t.Fatalf("expected: %v, got: %v", "json", format.Kind())
	}
}
