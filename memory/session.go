// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
)

// Session contains the state of a specific conversation with an agent which may include:
//
//   - Conversation history or a reference to externally stored conversation history.
//   - Memories or a reference to externally stored memories.
//   - Any other state that the agent needs to persist across runs for a conversation.
//
// A Session may also have behaviors attached to it that may include:
//
//   - Customized storage of state.
//   - Data extraction from and injection into a conversation.
//   - Chat history reduction, e.g. where messages needs to be summarized or truncated to reduce the size.
//
// A Session is always constructed by an [agent.Agent] so that the [agent.Agent] can attach any necessary behaviors to the Session.
// See the [agent.Agent.CreateSession], [agent.Agent.MarshalSession], and [agent.Agent.UnmarshalSession] methods for more information.
//
// Because of these behaviors, a Session may not be reusable across different agents, since each agent may add different
// behaviors to the Session it creates.
//
// To support conversations that may need to survive application restarts or separate service requests,
// a Session can be serialized and deserialized, so that it can be saved in a persistent store.
type Session struct {
	ServiceID string

	mu    sync.RWMutex
	state map[string]*stateValue
	id    string
}

func NewSession(id string) *Session {
	if id == "" {
		id = uuid.NewString()
	}
	return &Session{
		id: id,
	}
}

func (s *Session) ID() string {
	return s.id
}

// Get decodes the value associated with key into value and reports whether the key was present.
// value must be a non-nil pointer to the desired destination type.
func (s *Session) Get(key string, value any) (bool, error) {
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

	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state, key)
}

func (s *Session) MarshalJSON() ([]byte, error) {
	tmp := struct {
		State map[string]*stateValue

		ID        string
		ServiceID string
	}{
		ID:        s.id,
		ServiceID: s.ServiceID,
	}
	s.mu.RLock()
	if s.state == nil {
		tmp.State = make(map[string]*stateValue)
	} else {
		tmp.State = s.state
	}
	s.mu.RUnlock()
	return json.Marshal(tmp)
}

func (s *Session) UnmarshalJSON(data []byte) error {
	var tmp struct {
		State     map[string]*stateValue
		ID        string
		ServiceID string
	}
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	s.id = tmp.ID
	s.ServiceID = tmp.ServiceID
	if tmp.State == nil {
		tmp.State = make(map[string]*stateValue)
	}
	s.mu.Lock()
	s.state = tmp.State
	s.mu.Unlock()
	return nil
}
