// Copyright (c) Microsoft. All rights reserved.

package jsonformat_test

import (
	"testing"

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
		format, err := jsonformat.For[SimpleStruct](nil)
		if err != nil {
			t.Fatal(err)
		}
		if format.Name != "SimpleStruct" {
			t.Fatalf("expected: %v, got: %v", "SimpleStruct", format.Name)
		}
		if format.Kind() != "json" {
			t.Fatalf("expected: %v, got: %v", "json", format.Kind())
		}
	})

	t.Run("WithCustomName", func(t *testing.T) {
		opts := &jsonformat.Options{
			Name:        "CustomName",
			Description: "A custom description",
		}
		format, err := jsonformat.For[SimpleStruct](opts)
		if err != nil {
			t.Fatal(err)
		}
		if format.Name != "CustomName" {
			t.Fatalf("expected: %v, got: %v", "CustomName", format.Name)
		}
		if format.Description != "A custom description" {
			t.Fatalf("expected: %v, got: %v", "A custom description", format.Description)
		}
	})

	t.Run("WithStrict", func(t *testing.T) {
		opts := &jsonformat.Options{
			Strict: true,
		}
		format, err := jsonformat.For[SimpleStruct](opts)
		if err != nil {
			t.Fatal(err)
		}
		if !format.Strict {
			t.Fatal("expected Strict to be true")
		}
	})

	t.Run("AnyType", func(t *testing.T) {
		format, err := jsonformat.For[any](nil)
		if err != nil {
			t.Fatal(err)
		}
		if format.Schema.Type != "object" {
			t.Fatalf("expected: %v, got: %v", "object", format.Schema.Type)
		}
	})

	t.Run("PointerType", func(t *testing.T) {
		format, err := jsonformat.For[*SimpleStruct](nil)
		if err != nil {
			t.Fatal(err)
		}
		// Pointer types don't have a name by default
		if format.Name != "" {
			t.Fatalf("expected: %v, got: %v", "", format.Name)
		}
	})

	t.Run("PrimitiveTypes", func(t *testing.T) {
		t.Run("String", func(t *testing.T) {
			_, err := jsonformat.For[string](nil)
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("Int", func(t *testing.T) {
			_, err := jsonformat.For[int](nil)
			if err != nil {
				t.Fatal(err)
			}
		})

		t.Run("Bool", func(t *testing.T) {
			_, err := jsonformat.For[bool](nil)
			if err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("NestedStruct", func(t *testing.T) {
		format, err := jsonformat.For[NestedStruct](nil)
		if err != nil {
			t.Fatal(err)
		}
		if format.Name != "NestedStruct" {
			t.Fatalf("expected: %v, got: %v", "NestedStruct", format.Name)
		}
	})
}

func TestMustFor(t *testing.T) {
	assertNotPanics(t, func() {
		jsonformat.MustFor[SimpleStruct](nil)
	})
}

func TestFormatKind(t *testing.T) {
	format := jsonformat.MustFor[SimpleStruct](nil)
	if format.Kind() != "json" {
		t.Fatalf("expected: %v, got: %v", "json", format.Kind())
	}
}

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic, but function did not panic")
		}
	}()
	fn()
}

func assertNotPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("expected no panic, but got panic: %v", r)
		}
	}()
	fn()
}
