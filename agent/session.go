// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"
	"errors"
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
// A Session is always constructed by an [Agent] so that the [Agent] can attach any necessary behaviors to the Session.
// See the [Agent.CreateSession], [Agent.MarshalSession], and [Agent.UnmarshalSession] methods for more information.
//
// Because of these behaviors, a Session may not be reusable across different agents, since each agent may add different
// behaviors to the Session it creates.
//
// To support conversations that may need to survive application restarts or separate service requests,
// a Session can be serialized and deserialized through [Agent.MarshalSession] and
// [Agent.UnmarshalSession], so that it can be saved in a persistent store.
type Session interface {
	// Get attempts to read the value associated with key into value.
	//
	// It returns ok=true only when the value exists and can be read into the destination type.
	// It returns ok=false with a nil error when the key is missing or the stored value cannot be read as the
	// requested type.
	//
	// value must be a non-nil pointer to the desired destination type.
	Get(key string, value any) (ok bool, err error)

	// Set stores a value in the session state under the given key.
	// If the key already exists, its value is overwritten.
	Set(key string, value any)

	// Delete removes the value with the given key.
	Delete(key string)

	// ServiceID returns the provider-specific identifier associated with the session.
	ServiceID() string

	// SetServiceID sets the provider-specific identifier associated with the session.
	SetServiceID(id string)

	// sealedSession prevents implementations outside this package so sessions can only
	// be created by an Agent. This also allows Session to grow with new methods without
	// breaking existing implementations.
	sealedSession()
}

type session struct {
	serviceID string

	state map[string]*stateValue
}

func (s *session) sealedSession() {}

func (s *session) Get(key string, value any) (bool, error) {
	if s == nil {
		return false, nil
	}
	wrapped, ok := s.state[key]
	if !ok {
		return false, nil
	}
	return wrapped.readInto(value)
}

func (s *session) Set(key string, value any) {
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

func (s *session) Delete(key string) {
	if s == nil {
		return
	}
	delete(s.state, key)
}

func (s *session) ServiceID() string {
	if s == nil {
		return ""
	}
	return s.serviceID
}

func (s *session) SetServiceID(id string) {
	if s == nil {
		return
	}
	s.serviceID = id
}

func (s *session) MarshalJSON() ([]byte, error) {
	return nil, errors.New("sessions must be marshaled with Agent.MarshalSession")
}

func (s *session) UnmarshalJSON([]byte) error {
	return errors.New("sessions must be unmarshaled with Agent.UnmarshalSession")
}

type sessionData struct {
	State map[string]*stateValue

	ServiceID string
}

func marshalSession(sess Session) ([]byte, error) {
	s, ok := sess.(*session)
	if !ok || s == nil {
		return nil, errors.New("the provided session is nil")
	}
	tmp := struct {
		State map[string]*stateValue

		ServiceID string
	}{
		ServiceID: s.serviceID,
	}
	if s.state == nil {
		tmp.State = make(map[string]*stateValue)
	} else {
		tmp.State = s.state
	}
	return json.Marshal(tmp)
}

func unmarshalSession(data []byte) (Session, error) {
	var tmp sessionData
	if err := json.Unmarshal(data, &tmp); err != nil {
		return nil, err
	}
	if tmp.State == nil {
		tmp.State = make(map[string]*stateValue)
	}
	return &session{
		serviceID: tmp.ServiceID,
		state:     tmp.State,
	}, nil
}
