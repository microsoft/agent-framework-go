// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// stateValue wraps a session state value in either serialized or deserialized form,
// and supports lazy deserialization with type-aware caching.
type stateValue struct {
	mu        sync.Mutex
	raw       json.RawMessage
	cached    any
	cachedTyp reflect.Type
}

// newStateValue creates a new state value from a deserialized value.
func newStateValue(value any) *stateValue {
	v := &stateValue{}
	v.setDeserialized(value)
	return v
}

// newStateValueFromJSON creates a new state value from serialized JSON.
func newStateValueFromJSON(raw json.RawMessage) *stateValue {
	copyRaw := append(json.RawMessage(nil), raw...)
	return &stateValue{raw: copyRaw}
}

// SetDeserialized updates the cached deserialized value.
func (v *stateValue) setDeserialized(value any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cached = value
	v.cachedTyp = reflect.TypeOf(value)
	v.raw = nil
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

	v.mu.Lock()
	defer v.mu.Unlock()

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

// tryReadDeserializedValue attempts to read and cache the value as T.
// It returns false if the value is undefined, type-incompatible, or cannot be deserialized as T.
func tryReadDeserializedValue[T any](v *stateValue) (T, bool) {
	result, err := readDeserializedValue[T](v)
	if err != nil {
		var zero T
		return zero, false
	}
	return result, true
}

// readDeserializedValue reads and caches the value as T.
func readDeserializedValue[T any](v *stateValue) (T, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	requestedType := reflect.TypeFor[T]()
	if v.cachedTyp != nil {
		if v.cachedTyp == requestedType {
			cast, ok := v.cached.(T)
			if !ok {
				var zero T
				return zero, fmt.Errorf("cached value type mismatch for %v", requestedType)
			}
			return cast, nil
		}
		var zero T
		return zero, fmt.Errorf("cached value type is %v, requested %v", v.cachedTyp, requestedType)
	}

	if v.raw == nil {
		var zero T
		return zero, fmt.Errorf("value is undefined")
	}

	var decoded T
	if err := json.Unmarshal(v.raw, &decoded); err != nil {
		var zero T
		return zero, err
	}
	v.cached = decoded
	v.cachedTyp = requestedType
	return decoded, nil
}

func (v *stateValue) MarshalJSON() ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cachedTyp != nil {
		return json.Marshal(v.cached)
	}
	if v.raw != nil {
		return append([]byte(nil), v.raw...), nil
	}
	return []byte("null"), nil
}

func (v *stateValue) UnmarshalJSON(data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.raw = append(json.RawMessage(nil), data...)
	v.cached = nil
	v.cachedTyp = nil
	return nil
}
