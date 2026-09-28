// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
)

// stateValue wraps a session state value in either serialized or deserialized form,
// and supports lazy deserialization with type-aware caching.
type stateValue struct {
	raw       json.RawMessage
	cached    any
	cachedTyp reflect.Type
	hasCached bool
}

// newStateValue creates a new state value from a deserialized value.
func newStateValue(value any) *stateValue {
	return &stateValue{cached: value, cachedTyp: reflect.TypeOf(value), hasCached: true}
}

func (v *stateValue) readInto(out any) (bool, error) {
	if out == nil {
		return false, fmt.Errorf("out must be a non-nil pointer")
	}
	outValue := reflect.ValueOf(out)
	if outValue.Kind() != reflect.Pointer || outValue.IsNil() {
		return false, fmt.Errorf("out must be a non-nil pointer")
	}
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
	// Prefer the original raw JSON when present: it is the authoritative
	// serialized form for a deserialized value and preserves fields that were
	// never read. Reading a value caches a typed copy (readInto), but that copy
	// may be a narrower/partial view, so re-encoding it would silently drop the
	// unread fields on the next save. Values created via Set have raw == nil and
	// marshal losslessly from cached.
	if v.raw != nil {
		return slices.Clone(v.raw), nil
	}
	if v.hasCached {
		return json.Marshal(v.cached)
	}
	return []byte("null"), nil
}

func (v *stateValue) UnmarshalJSON(data []byte) error {
	v.raw = slices.Clone(json.RawMessage(data))
	v.cached = nil
	v.cachedTyp = nil
	v.hasCached = false
	return nil
}
