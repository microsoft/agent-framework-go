// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"encoding/json"

	"github.com/microsoft/agent-framework-go/agent/memory"
)

var _ memory.Session = (*Session)(nil)

// Session is a lightweight session container for chat agents.
// It holds only the conversation identifier and a [memory.StateBag] for session-scoped provider state.
// Providers (message history and context) are owned by the agent, not the session.
type Session struct {
	ConversationID string
	State          memory.StateBag
}

// GetStateBag returns the session's [memory.StateBag] for storing session-scoped provider state.
func (t *Session) GetStateBag() *memory.StateBag { return &t.State }

func newSessionFromJSON(data []byte) (*Session, error) {
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}
