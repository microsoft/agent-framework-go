// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/format"
	rootjsonformat "github.com/microsoft/agent-framework-go/format/jsonformat"
)

// Format provides JSON schema-based response formatting and validation.
//
// Deprecated: Use [rootjsonformat.Format] instead.
type Format = rootjsonformat.Format

// New creates a new JSON response format with the given name, description, and schema.
//
// Deprecated: Use [rootjsonformat.New] instead.
func New(name, description string, schema *jsonschema.Schema) format.ResponseFormat {
	return rootjsonformat.New(name, description, schema)
}

// FromResponseFormat creates a Format from a response format data value.
//
// Deprecated: Use [rootjsonformat.FromResponseFormat] instead.
func FromResponseFormat(format format.ResponseFormat) (*Format, error) {
	return rootjsonformat.FromResponseFormat(format)
}

// Any returns a response format with no schema constraints.
//
// Deprecated: Use [rootjsonformat.Any] instead.
func Any() format.ResponseFormat {
	return rootjsonformat.Any()
}

// Nothing returns a response format that matches no values.
//
// Deprecated: Use [rootjsonformat.Nothing] instead.
func Nothing() format.ResponseFormat {
	return rootjsonformat.Nothing()
}

// MustFor calls [For] and panics on error.
//
// Deprecated: Use [rootjsonformat.MustFor] instead.
func MustFor[T any]() format.ResponseFormat {
	return rootjsonformat.MustFor[T]()
}

// For creates a response format for the type T.
//
// Deprecated: Use [rootjsonformat.For] instead.
func For[T any]() (format.ResponseFormat, error) {
	return rootjsonformat.For[T]()
}

// ForType creates a response format for the given reflect.Type.
//
// Deprecated: Use [rootjsonformat.ForType] instead.
func ForType(rt reflect.Type) (format.ResponseFormat, error) {
	return rootjsonformat.ForType(rt)
}

// WrapNonObjectSchema returns a response format with an object-root schema.
//
// Deprecated: Use [rootjsonformat.WrapNonObjectSchema] instead.
func WrapNonObjectSchema(responseFormat format.ResponseFormat) (format.ResponseFormat, error) {
	return rootjsonformat.WrapNonObjectSchema(responseFormat)
}
