// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
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
type Session struct {
	serviceID string

	state map[string]*stateValue
}

// Get attempts to read the value associated with key into value.
//
// It returns ok=true only when the value exists and can be read into the destination type.
// It returns ok=false with a nil error when the key is missing or the stored value cannot be read as the
// requested type.
//
// value must be a non-nil pointer to the desired destination type.
func (s *Session) Get(key string, value any) (bool, error) {
	if s == nil {
		return false, nil
	}
	wrapped, ok := s.state[key]
	if !ok {
		return false, nil
	}
	return wrapped.readInto(value)
}

// Set stores a value in the session state under the given key.
// If the key already exists, its value is overwritten.
func (s *Session) Set(key string, value any) {
	if s == nil {
		return
	}
	wrapped, ok := value.(*stateValue)
	if !ok {
		wrapped = newStateValue(value)
	}

	if s.state == nil {
		s.state = make(map[string]*stateValue)
	}
	s.state[key] = wrapped
}

// Delete removes the value with the given key.
func (s *Session) Delete(key string) {
	if s == nil {
		return
	}
	delete(s.state, key)
}

// ServiceID returns the provider-specific identifier associated with the session.
func (s *Session) ServiceID() string {
	if s == nil {
		return ""
	}
	return s.serviceID
}

// SetServiceID sets the provider-specific identifier associated with the session.
func (s *Session) SetServiceID(id string) {
	if s == nil {
		return
	}
	s.serviceID = id
}

func (s Session) MarshalJSON() ([]byte, error) {
	tmp := sessionData{
		ServiceID: s.serviceID,
	}
	if s.state == nil {
		tmp.State = make(map[string]*stateValue)
	} else {
		tmp.State = s.state
	}
	return json.Marshal(tmp)
}

func (s *Session) UnmarshalJSON(data []byte) error {
	var tmp sessionData
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	if tmp.State == nil {
		tmp.State = make(map[string]*stateValue)
	}
	s.serviceID = tmp.ServiceID
	s.state = tmp.State
	return nil
}

type sessionData struct {
	State map[string]*stateValue

	ServiceID string
}
