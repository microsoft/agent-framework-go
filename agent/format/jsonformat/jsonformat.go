// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/agent"
)

// Format provides JSON schema-based response formatting and validation.
type Format struct {
	agent.ResponseFormat

	resolvedOnce sync.Once
	resolved     *jsonschema.Resolved
	resolvedErr  error
}

func (f *Format) resolvedSchema() (*jsonschema.Resolved, error) {
	f.resolvedOnce.Do(func() {
		f.resolved, f.resolvedErr = f.Schema.(*jsonschema.Schema).Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	})
	return f.resolved, f.resolvedErr
}

func newFormat(name, description string, schema *jsonschema.Schema) *Format {
	return &Format{
		ResponseFormat: agent.ResponseFormat{
			Kind:        "json",
			Name:        name,
			Description: description,
			Strict:      true,
			Schema:      schema,
		},
	}
}

// New creates a new JSON response format with the given name, description, and schema.
func New(name, description string, schema *jsonschema.Schema) agent.ResponseFormat {
	return newFormat(name, description, schema).ResponseFormat
}

// FromResponseFormat creates a Format from a response format data value.
func FromResponseFormat(format agent.ResponseFormat) (*Format, error) {
	schema, ok := format.Schema.(*jsonschema.Schema)
	if !ok {
		return nil, fmt.Errorf("response format schema has type %T, want *jsonschema.Schema", format.Schema)
	}
	return newFormat(format.Name, format.Description, schema), nil
}

// Any returns a ResponseFormat representing the any type (no schema constraints).
func Any() agent.ResponseFormat {
	return New("any", "", &jsonschema.Schema{})
}

// Nothing returns a ResponseFormat that matches no values.
func Nothing() agent.ResponseFormat {
	return New("empty", "", &jsonschema.Schema{
		Not: &jsonschema.Schema{},
	})
}

// MustFor calls [For] and panics on error.
func MustFor[T any]() agent.ResponseFormat {
	f, err := For[T]()
	if err != nil {
		panic(err)
	}
	return f
}

// For creates a ResponseFormat for the type T.
func For[T any]() (agent.ResponseFormat, error) {
	return ForType(reflect.TypeFor[T]())
}

// ForType creates a ResponseFormat for the given reflect.Type.
// A nil rt is treated as [Nothing].
func ForType(rt reflect.Type) (agent.ResponseFormat, error) {
	var schema *jsonschema.Schema
	if rt == nil {
		return Nothing(), nil
	}
	// Pointers are treated equivalently to non-pointers when deriving the schema.
	// The only difference is whether the value can be null. We don't want that
	// to happen for the root object, as that complicates usage with no benefit.
	rt = dereference(rt)
	schema, err := jsonschema.ForType(rt, &jsonschema.ForOptions{})
	if err != nil {
		return agent.ResponseFormat{}, err
	}
	name := rt.String()
	if split := strings.Split(name, "."); len(split) != 0 {
		// Use only the type name, not the package path.
		name = split[len(split)-1]
	}
	return New(name, "", schema), nil
}

func dereference(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
