// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"encoding/json"
	"reflect"
)

// PortableValue represents a value that can be exported / imported to a workflow,
// e.g. through an external request/response, or through checkpointing.
// The zero PortableValue corresponds to nil.
type PortableValue struct {
	_      [0]func() // disallow ==
	any    any
	TypeID TypeID
	// TODO: Optimize small values so they avoid allocations, like in slog.Value.
}

// AnyPortableValue returns a [PortableValue] for the supplied value.
//
// If the supplied value is of type PortableValue, it is returned
// unmodified.
func AnyPortableValue(v any) PortableValue {
	switch val := v.(type) {
	case PortableValue:
		return val
	default:
		return PortableValue{any: v}
	}
}

// PortableValueAs attempts to convert the supplied [PortableValue] to the requested type T.
func PortableValueAs[T any](v PortableValue) (T, bool) {
	if reflect.TypeFor[T]() == reflect.TypeFor[PortableValue]() {
		return any(v).(T), true
	}
	if !v.Is(reflect.TypeFor[T]()) {
		var zero T
		return zero, false
	}
	return v.Any().(T), true
}

func (v *PortableValue) IsZero() bool {
	return v == nil || v.any == nil
}

// Any returns v's value as an any.
func (v *PortableValue) Any() any {
	return v.any
}

// Is reports whether the value is of the specified type.
// If the value is stored in a delayed deserialized form, it will attempt to
// deserialize it to the requested type.
func (v *PortableValue) Is(typ reflect.Type) bool {
	if v == nil {
		return false
	}
	if typ == reflect.TypeFor[PortableValue]() {
		return true
	}
	raw, ok := v.any.(json.RawMessage)
	if !ok {
		// not a delayed deserialization
		return reflect.TypeOf(v.any).AssignableTo(typ)
	}
	target := reflect.New(typ).Interface()
	// Either we have no cache, or the types are incompatible; see if we can deserialize to the requested type
	if err := json.Unmarshal(raw, target); err != nil {
		return false
	}
	v.any = target
	return true
}

func (v *PortableValue) As(typ reflect.Type) (any, bool) {
	if v.Is(typ) {
		return v.any, true
	}
	return nil, false
}

// Delayed reports whether the value is stored in a delayed deserialized form.
func (v *PortableValue) Delayed() bool {
	if v.any == nil {
		return false
	}
	_, ok := v.any.(json.RawMessage)
	return ok
}
