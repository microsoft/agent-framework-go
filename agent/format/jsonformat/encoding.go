// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// Unmarshal unmarshals data into v according to the provided format,
// validating against the format's schema.
func (f *Format) Unmarshal(data []byte, v any) error {
	resolved, err := f.resolvedSchema()
	if err != nil {
		return err
	}
	if len(data) == 0 {
		// Unmarshaling to "null" is the wrong behavior,
		// better to treat empty input as an empty object.
		data = []byte("{}")
	}
	data, err = applySchema(data, resolved)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshaling: %w", err)
	}
	return nil
}

// Marshal marshals v into JSON according to the provided format,
// validating against the format's schema.
func (f *Format) Marshal(v any) ([]byte, error) {
	resolved, err := f.resolvedSchema()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling: %w", err)
	}
	return applySchema(json.RawMessage(data), resolved)
}

// Normalize applies schema defaults to v and validates it against the format's schema.
//
// v must be a non-nil pointer to the value being normalized.
func (f *Format) Normalize(v any) error {
	if v == nil {
		return fmt.Errorf("normalizing: nil target")
	}

	resolved, err := f.resolvedSchema()
	if err != nil {
		return err
	}
	if err := resolved.ApplyDefaults(v); err != nil {
		return fmt.Errorf("applying schema defaults: %w", err)
	}
	if err := validate(v, resolved); err != nil {
		return fmt.Errorf("normalizing: %w", err)
	}
	return nil
}

func applySchema(data json.RawMessage, resolved *jsonschema.Resolved) (json.RawMessage, error) {
	var v any
	if len(data) > 0 {
		// Decode with UseNumber so integers beyond 2^53 are not silently
		// truncated by being decoded into float64 and re-marshalled.
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("unmarshaling arguments: %w", err)
		}
	}
	if err := resolved.ApplyDefaults(&v); err != nil {
		return nil, fmt.Errorf("applying schema defaults: %w", err)
	}
	if err := validate(v, resolved); err != nil {
		return nil, err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshalling with defaults: %w", err)
	}
	return data, nil
}

func validate(v any, resolved *jsonschema.Resolved) error {
	// Validating against a Go object doesn't work well, better
	// to marshal back to JSON and validate against that, which also has
	// the benefit of supporting custom JSON marshalling.
	// See https://github.com/google/jsonschema-go/issues/23.
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling for validation: %w", err)
	}
	var validatedValue any
	if err := json.Unmarshal(data, &validatedValue); err != nil {
		return fmt.Errorf("unmarshaling for validation: %w", err)
	}
	if err := resolved.Validate(validatedValue); err != nil {
		return fmt.Errorf("validation error: %w", err)
	}
	return nil
}
