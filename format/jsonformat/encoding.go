// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// Unmarshal unmarshals data into v according to the provided format,
// validating against the format's schema.
func Unmarshal(format *Format, data []byte, v any) error {
	resolved, err := format.ResolvedSchema()
	if err != nil {
		return err
	}
	data, err = applySchema(data, resolved)
	if err != nil {
		return fmt.Errorf("validating: %w", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshaling: %w", err)
	}
	return nil
}

// Marshal marshals v into JSON according to the provided format,
// validating against the format's schema.
func Marshal(format *Format, v any) ([]byte, error) {
	resolved, err := format.ResolvedSchema()
	if err != nil {
		return nil, err
	}
	outbytes, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling: %w", err)
	}
	// Validate the output JSON, and apply defaults.
	//
	// We validate against the JSON, rather than the output value, as
	// some types may have custom JSON marshalling.
	outJSON, err := applySchema(json.RawMessage(outbytes), resolved)
	if err != nil {
		return nil, fmt.Errorf("validating: %w", err)
	}
	return outJSON, nil
}

// applySchema validates whether data is valid JSON according to the provided
// schema, after applying schema defaults.
//
// Returns the JSON value augmented with defaults.
func applySchema(data json.RawMessage, resolved *jsonschema.Resolved) (json.RawMessage, error) {
	var v any

	// For primitive types (non-object, non-array), unmarshal directly into the appropriate type
	if len(data) > 0 {
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("unmarshaling arguments: %w", err)
		}
	}

	// ApplyDefaults and Validate work on the unmarshaled value
	if err := resolved.ApplyDefaults(&v); err != nil {
		return nil, fmt.Errorf("applying schema defaults: %w", err)
	}

	if err := resolved.Validate(&v); err != nil {
		return nil, err
	}

	// We must re-marshal with the default values applied.
	var err error
	data, err = json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshalling with defaults: %w", err)
	}
	return data, nil
}
