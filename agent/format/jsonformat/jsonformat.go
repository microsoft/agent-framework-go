// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
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
		schema, ok := f.Schema.(*jsonschema.Schema)
		if !ok {
			f.resolvedErr = fmt.Errorf("response format schema has type %T, want *jsonschema.Schema", f.Schema)
			return
		}
		// The stored schema is strict (every property is required, with optional
		// properties made nullable). That form is required by OpenAI when sending,
		// but it is too rigid for local validation of Go values, which omit
		// omitempty fields. Relax the strict-only requirements before resolving so
		// validation keeps matching the original optionality.
		validationSchema, err := relaxStrict(schema)
		if err != nil {
			f.resolvedErr = err
			return
		}
		f.resolved, f.resolvedErr = validationSchema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	})
	return f.resolved, f.resolvedErr
}

func newFormat(name, description string, schema *jsonschema.Schema) *Format {
	// OpenAI strict structured outputs require every key present in an object's
	// "properties" to also appear in its "required" array. The jsonschema-go
	// inference omits omitempty/omitzero fields from "required", so rewrite the
	// schema to mark all properties required, expressing optionality via nullable
	// types instead. This mirrors .NET's
	// AIJsonSchemaCreateOptions.RequireAllProperties = true.
	makeStrict(schema)
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

// walkSchema visits s and every subschema reachable from it, calling fn on each.
func walkSchema(s *jsonschema.Schema, fn func(*jsonschema.Schema)) {
	if s == nil {
		return
	}
	fn(s)
	for _, sub := range s.Properties {
		walkSchema(sub, fn)
	}
	walkSchema(s.Items, fn)
	for _, sub := range s.PrefixItems {
		walkSchema(sub, fn)
	}
	walkSchema(s.AdditionalProperties, fn)
	for _, sub := range s.AnyOf {
		walkSchema(sub, fn)
	}
	for _, sub := range s.AllOf {
		walkSchema(sub, fn)
	}
	for _, sub := range s.OneOf {
		walkSchema(sub, fn)
	}
	for _, def := range s.Defs {
		walkSchema(def, fn)
	}
	for _, def := range s.Definitions {
		walkSchema(def, fn)
	}
}

// makeStrict recursively rewrites schema so that every object lists all of its
// properties in "required". Properties that were not originally required are
// made nullable so the model can still signal an absent value. This mirrors
// .NET's AIJsonSchemaCreateOptions.RequireAllProperties = true and produces a
// schema OpenAI accepts for strict structured outputs.
func makeStrict(s *jsonschema.Schema) {
	walkSchema(s, func(s *jsonschema.Schema) {
		if len(s.Properties) == 0 {
			return
		}
		originalRequired := make(map[string]bool, len(s.Required))
		for _, name := range s.Required {
			originalRequired[name] = true
		}
		required := make([]string, 0, len(s.Properties))
		seen := make(map[string]bool, len(s.Properties))
		// Preserve the inferred property order when available.
		for _, name := range s.PropertyOrder {
			if _, ok := s.Properties[name]; ok && !seen[name] {
				required = append(required, name)
				seen[name] = true
			}
		}
		rest := make([]string, 0, len(s.Properties))
		for name := range s.Properties {
			if !seen[name] {
				rest = append(rest, name)
			}
		}
		sort.Strings(rest)
		required = append(required, rest...)
		for _, name := range required {
			if !originalRequired[name] {
				makeNullable(s.Properties[name])
			}
		}
		s.Required = required
	})
}

// relaxStrict returns a deep copy of schema in which the strict-only additions
// made by makeStrict are undone for validation purposes: nullable (optional)
// properties are dropped from each object's "required" list. The input schema
// is left unchanged so the strict form is still sent to the provider.
func relaxStrict(schema *jsonschema.Schema) (*jsonschema.Schema, error) {
	clone, err := cloneSchema(schema)
	if err != nil {
		return nil, err
	}
	walkSchema(clone, func(s *jsonschema.Schema) {
		if len(s.Properties) == 0 || len(s.Required) == 0 {
			return
		}
		required := s.Required[:0]
		for _, name := range s.Required {
			if prop := s.Properties[name]; prop != nil && isNullable(prop) {
				continue
			}
			required = append(required, name)
		}
		s.Required = required
	})
	return clone, nil
}

// cloneSchema returns a deep copy of s via its JSON representation.
func cloneSchema(s *jsonschema.Schema) (*jsonschema.Schema, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("cloning schema: %w", err)
	}
	var out jsonschema.Schema
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("cloning schema: %w", err)
	}
	return &out, nil
}

// makeNullable marks s as accepting the JSON null value in addition to its
// existing type(s). In a strict schema every property stays required, so an
// originally optional property signals "no value" by being null rather than by
// being omitted.
func makeNullable(s *jsonschema.Schema) {
	if s == nil || isNullable(s) {
		return
	}
	switch {
	case s.Type != "":
		s.Types = []string{s.Type, "null"}
		s.Type = ""
	case len(s.Types) > 0:
		s.Types = append(s.Types, "null")
	}
}

// isNullable reports whether s permits the JSON null value.
func isNullable(s *jsonschema.Schema) bool {
	if s.Type == "null" {
		return true
	}
	for _, t := range s.Types {
		if t == "null" {
			return true
		}
	}
	return false
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
