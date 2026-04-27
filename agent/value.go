// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// stateValue wraps a session state value in either serialized or deserialized form,
// and supports lazy deserialization with type-aware caching.
type stateValue struct {
	raw       json.RawMessage
	cached    any
	cachedTyp reflect.Type
}

// newStateValue creates a new state value from a deserialized value.
func newStateValue(value any) *stateValue {
	return &stateValue{cached: value, cachedTyp: reflect.TypeOf(value)}
}

func (v *stateValue) readInto(out any) error {
	if out == nil {
		return fmt.Errorf("out must be a non-nil pointer")
	}
	outValue := reflect.ValueOf(out)
	if outValue.Kind() != reflect.Pointer || outValue.IsNil() {
		return fmt.Errorf("out must be a non-nil pointer")
	}
	requestedType := outValue.Elem().Type()

	if v.cachedTyp != nil {
		if v.cachedTyp != requestedType {
			return fmt.Errorf("cached value type is %v, requested %v", v.cachedTyp, requestedType)
		}
		cachedValue := reflect.ValueOf(v.cached)
		if !cachedValue.IsValid() {
			outValue.Elem().SetZero()
			return nil
		}
		outValue.Elem().Set(cachedValue)
		return nil
	}
	if v.raw == nil {
		return fmt.Errorf("value is undefined")
	}
	if err := json.Unmarshal(v.raw, out); err != nil {
		return err
	}
	v.cached = outValue.Elem().Interface()
	v.cachedTyp = requestedType
	return nil
}

func (v *stateValue) MarshalJSON() ([]byte, error) {
	if v.cachedTyp != nil {
		return json.Marshal(v.cached)
	}
	if v.raw != nil {
		return append([]byte(nil), v.raw...), nil
	}
	return []byte("null"), nil
}

func (v *stateValue) UnmarshalJSON(data []byte) error {
	v.raw = append(json.RawMessage(nil), data...)
	v.cached = nil
	v.cachedTyp = nil
	return nil
}
