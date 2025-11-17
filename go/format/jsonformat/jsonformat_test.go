// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework/go/format/jsonformat"
)

type SimpleStruct struct {
	Name  string `json:"name"`
	Age   int    `json:"age"`
	Email string `json:"email,omitempty"`
}

type NestedStruct struct {
	ID     string       `json:"id"`
	Person SimpleStruct `json:"person"`
	Active bool         `json:"active"`
}

type PointerStruct struct {
	Value *string `json:"value,omitempty"`
	Count *int    `json:"count,omitempty"`
}

func TestFor(t *testing.T) {
	t.Run("SimpleStruct", func(t *testing.T) {
		format, err := jsonformat.For[SimpleStruct]()
		if err != nil {
			t.Fatal(err)
		}
		if name := format.Name(); name != "SimpleStruct" {
			t.Fatalf("expected: %v, got: %v", "SimpleStruct", name)
		}
		if format.Kind() != "json" {
			t.Fatalf("expected: %v, got: %v", "json", format.Kind())
		}
	})

	t.Run("AnyType", func(t *testing.T) {
		format, err := jsonformat.For[any]()
		if err != nil {
			t.Fatal(err)
		}
		if got := format.Schema().(*jsonschema.Schema).Type; got != "" {
			t.Fatalf("expected empty type, got: %v", got)
		}
	})

	t.Run("PointerType", func(t *testing.T) {
		format, err := jsonformat.For[*SimpleStruct]()
		if err != nil {
			t.Fatal(err)
		}
		if name := format.Name(); name != "SimpleStruct" {
			t.Fatalf("expected: %v, got: %v", "SimpleStruct", name)
		}
	})

	t.Run("PrimitiveTypes", func(t *testing.T) {
		t.Run("String", func(t *testing.T) {
			_, err := jsonformat.For[string]()
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("Int", func(t *testing.T) {
			_, err := jsonformat.For[int]()
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("Bool", func(t *testing.T) {
			_, err := jsonformat.For[bool]()
			if err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("NestedStruct", func(t *testing.T) {
		format, err := jsonformat.For[NestedStruct]()
		if err != nil {
			t.Fatal(err)
		}
		if name := format.Name(); name != "NestedStruct" {
			t.Fatalf("expected: %v, got: %v", "NestedStruct", name)
		}
	})
}

func TestFormatKind(t *testing.T) {
	format := jsonformat.MustFor[SimpleStruct]()
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
