// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework/go/format"
)

// The jsonschema handling is loosely based on https://github.com/modelcontextprotocol/go-sdk

var _ format.Format = (*Format)(nil)

// Format implements the [format.Format] interface for JSON schema-based formats.
type Format struct {
	Name        string
	Description string
	Strict      bool
	Schema      *jsonschema.Schema

	resolvedSchema *jsonschema.Resolved
	zero           any
}

func (f *Format) Kind() string {
	return "json"
}

// Options for creating a [Format] or a [Value] for a type T.
type Options struct {
	jsonschema.ForOptions

	// Name is the name of the schema.
	//
	// If empty, the name of the type T is used.
	Name string

	// Description is the description of the schema.
	Description string

	// Strict indicates whether to enable strict validation
	// of the JSON schema and the strict adherence to the schema during
	// marshaling and unmarshaling.
	Strict bool
}

// MustFor calls [For] and panics on error.
func MustFor[T any](opts *Options) *Format {
	f, err := For[T](opts)
	if err != nil {
		panic(err)
	}
	return f
}

// For creates a Schema for the type T.
// If opts is nil, default options are used.
// The any type is treated as an empty object.
func For[T any](opts *Options) (*Format, error) {
	if opts == nil {
		opts = &Options{}
	}
	rt := reflect.TypeFor[T]()
	name := opts.Name
	if name == "" {
		name = rt.Name()
	}
	var schema *jsonschema.Schema
	var zero any
	if rt == reflect.TypeFor[any]() {
		// Special handling for an "any" input: treat as an empty object.
		schema = &jsonschema.Schema{Type: "object"}
	} else {
		if rt.Kind() == reflect.Pointer {
			// Pointers are treated equivalently to non-pointers when deriving the schema.
			// If an indirection occurred to derive the schema, a non-nil zero value is
			// returned to be used in place of the typed nil zero value.
			rt = rt.Elem()
			zero = reflect.Zero(rt).Interface()
		}
		var err error
		schema, err = jsonschema.ForType(rt, &opts.ForOptions)
		if err != nil {
			return nil, err
		}
	}
	// Always set ValidateDefaults to true when resolving the schema,
	// even when opts.Strict is false. The JSON Schema spec says it should
	// only be false for tools that generate schema visualizations.
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("resolving schema: %w", err)
	}
	return &Format{
		Name:           name,
		Description:    opts.Description,
		Schema:         schema,
		Strict:         opts.Strict,
		resolvedSchema: resolved,
		zero:           zero,
	}, nil
}

// applySchema validates whether data is valid JSON according to the provided
// schema, after applying schema defaults.
//
// Returns the JSON value augmented with defaults.
func applySchema(data json.RawMessage, resolved *jsonschema.Resolved, strict bool) (json.RawMessage, error) {
	var v any

	// For primitive types (non-object, non-array), unmarshal directly into the appropriate type
	if len(data) > 0 {
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("unmarshaling arguments: %w", err)
		}
	}

	// ApplyDefaults and Validate work on the unmarshaled value
	if err := resolved.ApplyDefaults(&v); err != nil {
		return nil, fmt.Errorf("applying schema defaults:\n%w", err)
	}

	if strict {
		if err := resolved.Validate(&v); err != nil {
			return nil, err
		}
	}

	// We must re-marshal with the default values applied.
	var err error
	data, err = json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshalling with defaults: %v", err)
	}
	return data, nil
}
