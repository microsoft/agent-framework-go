// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"encoding/json"
	"reflect"
)

// Value represents a value that can be exported / imported to a workflow,
// e.g. through an external request/response, or through checkpointing.
// The zero Value corresponds to nil.
type Value struct {
	_   [0]func() // disallow ==
	any any
	// TODO: Optimize small values so they avoid allocations, like in slog.Value.
}

// AnyValue returns a [Value] for the supplied value.
//
// If the supplied value is of type Value, it is returned
// unmodified.
func AnyValue(v any) Value {
	switch val := v.(type) {
	case Value:
		return val
	default:
		return Value{any: v}
	}
}

// ValueAs attempts to convert the supplied [Value] to the requested type T.
func ValueAs[T any](v Value) (T, bool) {
	if reflect.TypeFor[T]() == reflect.TypeFor[Value]() {
		return any(v).(T), true
	}
	if !v.Is(reflect.TypeFor[T]()) {
		var zero T
		return zero, false
	}
	return v.Any().(T), true
}

// Any returns v's value as an any.
func (v *Value) Any() any {
	return v.any
}

// Is reports whether the value is of the specified type.
// If the value is stored in a delayed deserialized form, it will attempt to
// deserialize it to the requested type.
func (v *Value) Is(typ reflect.Type) bool {
	if v == nil {
		return false
	}
	if typ == reflect.TypeFor[Value]() {
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

func (v *Value) As(typ reflect.Type) (any, bool) {
	if v.Is(typ) {
		return v.any, true
	}
	return nil, false
}

// Delayed reports whether the value is stored in a delayed deserialized form.
func (v *Value) Delayed() bool {
	if v.any == nil {
		return false
	}
	_, ok := v.any.(json.RawMessage)
	return ok
}
