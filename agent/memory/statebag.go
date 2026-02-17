// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"encoding/json"
	"sync"
)

// StateBag is a thread-safe key-value store for managing session-scoped state.
//
// StateBag enables storing and retrieving arbitrary values associated with a session using string keys.
// Context providers can use a StateBag to persist state across invocations within the same session.
//
// Since a [ContextProvider] is used with many different sessions, it should not store any
// session-specific information within its own instance fields. Instead, any session-specific state
// should be stored in the associated session's StateBag.
//
// A StateBag is safe for concurrent use by multiple goroutines.
type StateBag struct {
	state sync.Map
}

// Get returns the value associated with the given key and a boolean indicating whether the key was present.
// The returned value may be nil even when the key exists; callers must check the boolean result.
func (s *StateBag) Get(key string) (any, bool) {
	return s.state.Load(key)
}

// Set stores a value in the state bag under the given key.
// If the key already exists, its value is overwritten.
func (s *StateBag) Set(key string, value any) {
	s.state.Store(key, value)
}

// Delete removes the value with the given key.
func (s *StateBag) Delete(key string) {
	s.state.Delete(key)
}

// MarshalJSON serializes the StateBag to JSON.
func (s *StateBag) MarshalJSON() ([]byte, error) {
	m := make(map[string]any)
	s.state.Range(func(k, v any) bool {
		m[k.(string)] = v
		return true
	})
	return json.Marshal(m)
}

// UnmarshalJSON deserializes a JSON object into the StateBag.
func (s *StateBag) UnmarshalJSON(data []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	for k, v := range m {
		s.state.Store(k, v)
	}
	return nil
}
