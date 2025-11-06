// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"encoding/json"
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
func ValueAs[T any](v *Value) (T, bool) {
	if v == nil {
		var zero T
		return zero, false
	}

	tryDeserializeValue[T](v)

	as, ok := v.Any().(T)
	return as, ok
}

// Any returns v's value as an any.
func (v *Value) Any() any {
	return v.any
}

// Delayed reports whether the value is stored in a delayed deserialized form.
func (v *Value) Delayed() bool {
	if v.any == nil {
		return false
	}
	_, ok := v.any.(json.RawMessage)
	return ok
}

func tryDeserializeValue[T any](v *Value) {
	raw, ok := v.any.(json.RawMessage)
	if !ok {
		// not a delayed deserialization; nothing to do
		return
	}
	var target T
	// Either we have no cache, or the types are incompatible; see if we can deserialize to the requested type
	if err := json.Unmarshal(raw, &target); err != nil {
		return
	}
	v.any = target
}
