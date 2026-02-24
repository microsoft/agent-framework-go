// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

// StateBag is a thread-safe key-value store for managing session-scoped state.
//
// StateBag enables storing and retrieving arbitrary values associated with a session using string keys.
//
// A StateBag is safe for concurrent use by multiple goroutines.
type StateBag struct {
	mu    sync.RWMutex
	state map[string]*stateBagValue
}

// Get decodes the value associated with key into value and reports whether the key was present.
// value must be a non-nil pointer to the desired destination type.
func (s *StateBag) Get(key string, value any) (bool, error) {
	if s == nil {
		return false, nil
	}
	s.mu.RLock()
	wrapped, ok := s.state[key]
	s.mu.RUnlock()
	if !ok {
		return false, nil
	}
	return true, wrapped.readInto(value)
}

// Set stores a value in the state bag under the given key.
// If the key already exists, its value is overwritten.
func (s *StateBag) Set(key string, value any) {
	if s == nil {
		return
	}
	wrapped, ok := value.(*stateBagValue)
	if !ok {
		wrapped = newStateBagValue(value)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = make(map[string]*stateBagValue)
	}
	s.state[key] = wrapped
}

// Delete removes the value with the given key.
func (s *StateBag) Delete(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, key)
}

// MarshalJSON serializes the StateBag to JSON.
func (s *StateBag) MarshalJSON() ([]byte, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(s.state)
}

// UnmarshalJSON deserializes a JSON object into the StateBag.
func (s *StateBag) UnmarshalJSON(data []byte) error {
	if s == nil {
		return nil
	}
	var state map[string]*stateBagValue
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state == nil {
		state = make(map[string]*stateBagValue)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	return nil
}

// stateBagValue wraps a state bag value in either serialized or deserialized form,
// and supports lazy deserialization with type-aware caching.
type stateBagValue struct {
	mu        sync.Mutex
	raw       json.RawMessage
	cached    any
	cachedTyp reflect.Type
}

// newStateBagValue creates a new state bag value from a deserialized value.
func newStateBagValue(value any) *stateBagValue {
	v := &stateBagValue{}
	v.setDeserialized(value)
	return v
}

// newStateBagValueFromJSON creates a new state bag value from serialized JSON.
func newStateBagValueFromJSON(raw json.RawMessage) *stateBagValue {
	copyRaw := append(json.RawMessage(nil), raw...)
	return &stateBagValue{raw: copyRaw}
}

// SetDeserialized updates the cached deserialized value.
func (v *stateBagValue) setDeserialized(value any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cached = value
	v.cachedTyp = reflect.TypeOf(value)
	v.raw = nil
}

func (v *stateBagValue) readInto(out any) error {
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
func tryReadDeserializedValue[T any](v *stateBagValue) (T, bool) {
	result, err := readDeserializedValue[T](v)
	if err != nil {
		var zero T
		return zero, false
	}
	return result, true
}

// readDeserializedValue reads and caches the value as T.
func readDeserializedValue[T any](v *stateBagValue) (T, error) {
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

func (v *stateBagValue) MarshalJSON() ([]byte, error) {
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

func (v *stateBagValue) UnmarshalJSON(data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.raw = append(json.RawMessage(nil), data...)
	v.cached = nil
	v.cachedTyp = nil
	return nil
}
