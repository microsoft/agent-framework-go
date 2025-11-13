// Copyright (c) Microsoft. All rights reserved.

package jsonformat

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework/go/format"
)

var _ encoding.BinaryUnmarshaler = (*Value[any])(nil)
var _ format.FormatProvider = (*Value[any])(nil)

// A Value represents a value of type T along with its JSON Schema.
// It can marshal and unmarshal itself to and from JSON, validating against the schema.
// The zero Value corresponds to the zero value of type T.
type Value[T any] struct {
	value  T
	format *Format

	opts    *Options
	initErr error
}

// MustNewValue calls [NewValue] and panics on error.
func MustNewValue[T any](v T, opts *Options) *Value[T] {
	val, err := NewValue(v, opts)
	if err != nil {
		panic(err)
	}
	return val
}

// NewValue creates a new Value for type T.
// If opts is nil, default options are used.
// The any type is treated as an empty object.
func NewValue[T any](v T, opts *Options) (*Value[T], error) {
	val := &Value[T]{value: v, opts: opts}
	if err := val.init(opts); err != nil {
		return nil, err
	}
	return val, nil
}

func (v *Value[T]) init(opts *Options) error {
	if v.format == nil && v.initErr == nil {
		if reflect.TypeFor[T]() == reflect.TypeFor[any]() && any(v.value) != nil {
			v.initErr = errors.New("cannot create Value[any] with non-nil value")
			return v.initErr
		}
		v.format, v.initErr = For[T](opts)
	}
	return v.initErr
}

func (v *Value[T]) UnmarshalBinary(data []byte) error {
	return v.UnmarshalJSON(data)
}

func (v *Value[T]) UnmarshalJSON(data []byte) error {
	if err := v.init(v.opts); err != nil {
		return err
	}
	var err error
	data, err = applySchema(data, v.format.resolvedSchema, v.format.Strict)
	if err != nil {
		return fmt.Errorf("validating: %v", err)
	}
	// Unmarshal and validate args.
	if data != nil {
		if err := json.Unmarshal(data, &v.value); err != nil {
			return fmt.Errorf("unmarshaling: %w", err)
		}
	}
	return nil
}

func (v *Value[T]) MarshalJSON() ([]byte, error) {
	if err := v.init(v.opts); err != nil {
		return nil, err
	}
	// Marshal the output and put the RawMessage in the StructuredContent field.
	var outval any = v.value
	if v.format.zero != nil {
		// Avoid typed nil, which will serialize as JSON null.
		// Instead, use the zero value of the unpointered type.
		var z T
		if any(v.value) == any(z) { // zero is only non-nil if Out is a pointer type
			outval = v.format.zero
		}
	}
	if outval == nil {
		return nil, nil
	}
	outbytes, err := json.Marshal(outval)
	if err != nil {
		return nil, fmt.Errorf("marshaling: %w", err)
	}
	// Validate the output JSON, and apply defaults.
	//
	// We validate against the JSON, rather than the output value, as
	// some types may have custom JSON marshalling.
	outJSON, err := applySchema(json.RawMessage(outbytes), v.format.resolvedSchema, v.format.Strict)
	if err != nil {
		return nil, fmt.Errorf("validating: %w", err)
	}
	return outJSON, nil
}

// Wrap replaces the underlying value with val.
func (v *Value[T]) Wrap(val T) {
	v.value = val
}

// Unwrap returns the underlying value.
func (v *Value[T]) Unwrap() T {
	return v.value
}

// Format returns the [Format] associated with this value.
func (v *Value[T]) Format() (format.Format, error) {
	return v.FormatJSON()
}

// FormatJSON returns the [Format] associated with this value.
func (v *Value[T]) FormatJSON() (*Format, error) {
	if err := v.init(v.opts); err != nil {
		return nil, err
	}
	return v.format, nil
}

// MustFormat calls [Format] and panics on error.
func (v *Value[T]) MustFormat() format.Format {
	f, err := v.Format()
	if err != nil {
		panic(err)
	}
	return f
}
