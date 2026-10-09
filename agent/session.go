// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
)

// Session contains the state of a specific conversation with an agent which may include:
//
//   - Conversation history or a reference to externally stored conversation history.
//   - Memories or a reference to externally stored memories.
//   - Any other state that the agent needs to persist across runs for a conversation.
//
// Agent behaviors such as history and context providers live on the agent and store their state in the Session.
// The zero value is ready for sequential use. [Agent.CreateSession] can be used when a provider needs to configure
// provider-specific session state before the first run.
//
// Because provider-specific state can be associated with the agent that created it, a Session may not be reusable across
// different agents.
//
// Sessions support encoding/json, including marshaling by value. Use the same
// *Session throughout a conversation; copies are only for marshaling.
//
// Methods support concurrent use after initialization by [Agent.CreateSession],
// [Agent.Run], [Session.Set], [Session.SetServiceID], or successful JSON decoding.
// Initialize before sharing; read-modify-write sequences need separate synchronization.
//
// Marshaling takes a shallow snapshot; successful decoding replaces the state
// and service ID together. Stored values are not cloned. Synchronize mutations
// and use [Session.Set] to persist edits to restored values.
type Session struct {
	// state is initialized before concurrent use and never replaced.
	state *sessionState
}

type sessionState struct {
	mu        sync.RWMutex
	serviceID string
	values    map[string]*stateValue
}

func (s *Session) initState() *sessionState {
	if s.state == nil {
		s.state = new(sessionState)
	}
	return s.state
}

func validateSessionStateKey(key string) {
	if strings.TrimSpace(key) == "" {
		panic("session state key cannot be blank")
	}
}

// Get attempts to read the value associated with key into value.
//
// It returns ok=true only when the value exists and can be read into the destination type.
// It returns ok=false with a nil error when the key is missing or the stored value cannot be read as the
// requested type.
//
// value must be a non-nil pointer to the desired destination type.
// It panics if key is empty or whitespace.
func (s *Session) Get(key string, value any) (bool, error) {
	validateSessionStateKey(key)
	if s == nil || s.state == nil {
		return false, nil
	}
	state := s.state
	state.mu.RLock()
	wrapped, ok := state.values[key]
	state.mu.RUnlock()
	if !ok {
		return false, nil
	}
	return wrapped.readInto(value)
}

// Set stores a value in the session state under the given key.
// If the key already exists, its value is overwritten.
// It panics if key is empty or whitespace.
func (s *Session) Set(key string, value any) {
	validateSessionStateKey(key)
	if s == nil {
		return
	}
	wrapped, ok := value.(*stateValue)
	if !ok {
		wrapped = newStateValue(value)
	}

	state := s.initState()
	state.mu.Lock()
	if state.values == nil {
		state.values = make(map[string]*stateValue)
	}
	state.values[key] = wrapped
	state.mu.Unlock()
}

// Delete removes the value with the given key and reports whether it existed.
// It panics if key is empty or whitespace.
func (s *Session) Delete(key string) bool {
	validateSessionStateKey(key)
	if s == nil || s.state == nil {
		return false
	}
	state := s.state
	state.mu.Lock()
	_, ok := state.values[key]
	delete(state.values, key)
	state.mu.Unlock()
	return ok
}

// ServiceID returns the provider-specific identifier associated with the session.
// When [Config.RequirePerServiceCallHistoryPersistence] is enabled, it may instead
// identify history managed locally by the agent.
func (s *Session) ServiceID() string {
	if s == nil || s.state == nil {
		return ""
	}
	state := s.state
	state.mu.RLock()
	id := state.serviceID
	state.mu.RUnlock()
	return id
}

// SetServiceID sets the provider-specific identifier associated with the session.
func (s *Session) SetServiceID(id string) {
	if s == nil {
		return
	}
	state := s.initState()
	state.mu.Lock()
	state.serviceID = id
	state.mu.Unlock()
}

func (s Session) MarshalJSON() ([]byte, error) {
	var tmp sessionData
	if state := s.state; state != nil {
		state.mu.RLock()
		tmp.ServiceID = state.serviceID
		tmp.State = maps.Clone(state.values)
		state.mu.RUnlock()
	}
	if tmp.State == nil {
		tmp.State = make(map[string]*stateValue)
	}
	return json.Marshal(tmp)
}

func (s *Session) UnmarshalJSON(data []byte) error {
	var tmp struct {
		State     map[string]json.RawMessage
		ServiceID string
	}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	values := make(map[string]*stateValue, len(tmp.State))
	for key, raw := range tmp.State {
		values[key] = &stateValue{raw: slices.Clone(raw)}
	}
	state := s.initState()
	state.mu.Lock()
	state.serviceID = tmp.ServiceID
	state.values = values
	state.mu.Unlock()
	return nil
}

type sessionData struct {
	State map[string]*stateValue

	ServiceID string
}
