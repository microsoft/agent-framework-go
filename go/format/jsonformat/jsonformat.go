// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"reflect"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework/go/format"
)

var _ format.SchemaFormat = (*Format)(nil)

// Format implements the [format.Format] interface for JSON schema-based formats.
type Format struct {
	name        string
	description string
	schema      *jsonschema.Schema

	resolvedOnce sync.Once
	resolved     *jsonschema.Resolved
	resolvedErr  error
}

func (f *Format) Kind() string {
	return "json"
}

func (f *Format) Name() string {
	return f.name
}

func (f *Format) Description() string {
	return f.description
}

func (f *Format) Strict() bool {
	return true
}

func (f *Format) Schema() any {
	return f.schema
}

func (f *Format) ResolvedSchema() (*jsonschema.Resolved, error) {
	f.resolvedOnce.Do(func() {
		f.resolved, f.resolvedErr = f.schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	})
	return f.resolved, f.resolvedErr
}

// New creates a new Format with the given name, description, and schema.
func New(name, description string, schema *jsonschema.Schema) *Format {
	return &Format{
		name:        name,
		description: description,
		schema:      schema,
	}
}

// Any returns a Format representing the any type (no schema constraints).
func Any() *Format {
	return New("any", "", &jsonschema.Schema{})
}

// Nothing returns a Format that matches no values.
func Nothing() *Format {
	return New("empty", "", &jsonschema.Schema{
		Not: &jsonschema.Schema{},
	})
}

// MustFor calls [For] and panics on error.
func MustFor[T any]() *Format {
	f, err := For[T]()
	if err != nil {
		panic(err)
	}
	return f
}

// For creates a Schema for the type T.
func For[T any]() (*Format, error) {
	return ForType(reflect.TypeFor[T]())
}

// ForType creates a Schema for the given reflect.Type.
// A nil rt is treated as [Nothing].
func ForType(rt reflect.Type) (*Format, error) {
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
		return nil, err
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
