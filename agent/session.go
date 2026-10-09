// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/microsoft/agent-framework-go/internal/concurrent"
)

// Session contains the state of a specific conversation with an agent which may include:
//
//   - Conversation history or a reference to externally stored conversation history.
//   - Memories or a reference to externally stored memories.
//   - Any other state that the agent needs to persist across runs for a conversation.
//
// Agent behaviors such as history and context providers live on the agent and store their state in the Session.
// The zero value is ready to use. [Agent.CreateSession] can be used when a provider needs to configure
// provider-specific session state before the first run.
//
// Because provider-specific state can be associated with the agent that created it, a Session may not be reusable across
// different agents.
//
// To support conversations that may need to survive application restarts or separate service requests,
// a Session can be serialized and deserialized directly with encoding/json, so that it can be saved in a
// persistent store.
// Session methods support concurrent access to the state map and service ID. A
// JSON snapshot can reflect writes from different points during marshaling;
// UnmarshalJSON replaces the state and service ID together after decoding.
// Values stored in the session are not cloned; callers must coordinate mutations
// to their own values. Call Set after editing a value read from a deserialized
// session to persist the edit. Do not copy a Session after first use. Marshal
// a *Session, not a Session value, to preserve its JSON format.
type Session struct {
	current atomic.Pointer[sessionState]
}

type sessionState struct {
	serviceID string
	state     *concurrent.Map[string, *stateValue]
}

func (s *Session) stateForWrite() *sessionState {
	if current := s.current.Load(); current != nil {
		return current
	}
	initial := &sessionState{state: new(concurrent.Map[string, *stateValue])}
	if s.current.CompareAndSwap(nil, initial) {
		return initial
	}
	return s.current.Load()
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
	if s == nil {
		return false, nil
	}
	current := s.current.Load()
	if current == nil {
		return false, nil
	}
	wrapped, ok := current.state.Load(key)
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

	s.stateForWrite().state.Store(key, wrapped)
}

// Delete removes the value with the given key and reports whether it existed.
// It panics if key is empty or whitespace.
func (s *Session) Delete(key string) bool {
	validateSessionStateKey(key)
	if s == nil {
		return false
	}
	current := s.current.Load()
	if current == nil {
		return false
	}
	_, ok := current.state.LoadAndDelete(key)
	return ok
}

// ServiceID returns the provider-specific identifier associated with the session.
// When [Config.RequirePerServiceCallHistoryPersistence] is enabled, it may instead
// identify history managed locally by the agent.
func (s *Session) ServiceID() string {
	if s == nil {
		return ""
	}
	if current := s.current.Load(); current != nil {
		return current.serviceID
	}
	return ""
}

// SetServiceID sets the provider-specific identifier associated with the session.
func (s *Session) SetServiceID(id string) {
	if s == nil {
		return
	}
	for {
		current := s.current.Load()
		var state *concurrent.Map[string, *stateValue]
		if current == nil {
			state = new(concurrent.Map[string, *stateValue])
		} else {
			state = current.state
		}
		next := &sessionState{serviceID: id, state: state}
		if s.current.CompareAndSwap(current, next) {
			return
		}
	}
}

func (s *Session) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}

	tmp := sessionData{
		State: make(map[string]*stateValue),
	}
	if current := s.current.Load(); current != nil {
		tmp.ServiceID = current.serviceID
		maps.Insert(tmp.State, current.state.All())
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

	state := new(concurrent.Map[string, *stateValue])
	for key, raw := range tmp.State {
		state.Store(key, &stateValue{raw: slices.Clone(raw)})
	}
	s.current.Store(&sessionState{serviceID: tmp.ServiceID, state: state})
	return nil
}

type sessionData struct {
	State map[string]*stateValue

	ServiceID string
}
