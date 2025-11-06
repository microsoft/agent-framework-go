// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"encoding/json"
	"reflect"
)

// PortableValue represents a value that can be exported / imported to a workflow,
// e.g. through an external request/response, or through checkpointing.
type PortableValue struct {
	value             any
	deserializedValue any
}

func NewPortableValue(value any) *PortableValue {
	return &PortableValue{
		value: value,
	}
}

func PortableValueAs[T any](v *PortableValue) (T, bool) {
	tryDeserializeValue[T](v)

	as, ok := v.Value().(T)
	return as, ok
}

func PortableValueIs[T any](v *PortableValue) bool {
	tryDeserializeValue[T](v)

	_, ok := v.Value().(T)
	return ok
}

func (v *PortableValue) Value() any {
	if v.deserializedValue != nil {
		return v.deserializedValue
	}
	if v.value == nil {
		panic("nil value")
	}
	return v.value
}

func (v *PortableValue) DelayedDeserialization() bool {
	_, ok := v.value.(json.RawMessage)
	return ok
}

func tryDeserializeValue[T any](v *PortableValue) {
	raw, ok := v.value.(json.RawMessage)
	if !ok {
		// not a delayed deserialization; nothing to do
		return
	}
	var target T
	if v.deserializedValue != nil && reflect.TypeOf(v.deserializedValue).AssignableTo(reflect.TypeOf(target)) {
		// We have a cached deserialized value, and it's compatible with the requested type
		return
	}
	// Either we have no cache, or the types are incompatible; see if we can deserialize to the requested type
	if err := json.Unmarshal(raw, &target); err != nil {
		return
	}
	v.deserializedValue = target
}
