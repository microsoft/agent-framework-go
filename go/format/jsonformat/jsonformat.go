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

var _ format.SchemaFormat = (*Format)(nil)

// Format implements the [format.Format] interface for JSON schema-based formats.
type Format struct {
	name        string
	description string
	strict      bool
	schema      *jsonschema.Schema

	resolvedSchema *jsonschema.Resolved
	zero           any
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
	return f.strict
}

func (f *Format) Schema() any {
	return f.schema
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
func For[T any](opts *Options) (*Format, error) {
	return ForType(reflect.TypeFor[T](), opts)
}

// Nothing returns a Format that matches no values.
func Nothing() *Format {
	schema := &jsonschema.Schema{
		Not: &jsonschema.Schema{},
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		// should never happen
		panic(err)
	}
	return &Format{
		name:           "empty",
		schema:         schema,
		strict:         true,
		resolvedSchema: resolved,
	}
}

// Any returns a Format representing the any type (no schema constraints).
func Any() *Format {
	format, err := For[any](nil)
	if err != nil {
		// should never happen
		panic(err)
	}
	return format
}

// ForType creates a Schema for the given reflect.Type.
// If opts is nil, default options are used.
// A nil rt is treated as an empty object.
func ForType(rt reflect.Type, opts *Options) (*Format, error) {
	if opts == nil {
		opts = &Options{}
	}
	name := opts.Name
	var schema *jsonschema.Schema
	var zero any
	if rt == nil {
		schema = &jsonschema.Schema{Type: "object"}
	} else {
		if rt.Kind() == reflect.Pointer {
			// Pointers are treated equivalently to non-pointers when deriving the schema.
			// If an indirection occurred to derive the schema, a non-nil zero value is
			// returned to be used in place of the typed nil zero value.
			rt = rt.Elem()
			zero = reflect.Zero(rt).Interface()
		}
		if name == "" {
			name = rt.Name()
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
		name:           name,
		description:    opts.Description,
		schema:         schema,
		strict:         opts.Strict,
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
