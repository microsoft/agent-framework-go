// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

// stateValue wraps a session state value in either serialized or deserialized form,
// and supports lazy deserialization with type-aware caching.
type stateValue struct {
	mu sync.Mutex

	raw       json.RawMessage
	cached    any
	cachedTyp reflect.Type
	hasCached bool
}

// newStateValue creates a new state value from a deserialized value.
func newStateValue(value any) *stateValue {
	return &stateValue{cached: value, cachedTyp: reflect.TypeOf(value), hasCached: true}
}

func stateValueDestination(out any) (reflect.Value, error) {
	if out == nil {
		return reflect.Value{}, fmt.Errorf("out must be a non-nil pointer")
	}
	outValue := reflect.ValueOf(out)
	if outValue.Kind() != reflect.Pointer || outValue.IsNil() {
		return reflect.Value{}, fmt.Errorf("out must be a non-nil pointer")
	}
	return outValue, nil
}

func (v *stateValue) readInto(out any) (bool, error) {
	outValue, err := stateValueDestination(out)
	if err != nil {
		return false, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	requestedType := outValue.Elem().Type()

	if v.hasCached {
		if v.cachedTyp != requestedType {
			return false, nil
		}
		cachedValue := reflect.ValueOf(v.cached)
		if !cachedValue.IsValid() {
			outValue.Elem().SetZero()
			return true, nil
		}
		outValue.Elem().Set(cachedValue)
		return true, nil
	}
	if v.raw == nil {
		return false, nil
	}
	if err := json.Unmarshal(v.raw, out); err != nil {
		return false, nil
	}
	v.cached = outValue.Elem().Interface()
	v.cachedTyp = requestedType
	v.hasCached = true
	return true, nil
}

func (v *stateValue) MarshalJSON() ([]byte, error) {
	v.mu.Lock()
	raw, cached, hasCached := v.raw, v.cached, v.hasCached
	v.mu.Unlock()

	// Preserve the original raw JSON after a read: a narrower destination
	// type may not represent every field in the stored value. Set replaces
	// the raw value when a caller wants edits to be persisted.
	if raw != nil {
		return slices.Clone(raw), nil
	}
	if hasCached {
		return json.Marshal(cached)
	}
	return []byte("null"), nil
}

func (v *stateValue) UnmarshalJSON(data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.raw = slices.Clone(json.RawMessage(data))
	v.cached = nil
	v.cachedTyp = nil
	v.hasCached = false
	return nil
}
