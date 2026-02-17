// Copyright (c) Microsoft. All rights reserved.

package a2aagent

import "github.com/microsoft/agent-framework-go/agent/memory"

// Session represents a session identified by a service-managed identifier.
type Session struct {
	ContextID string
	TaskID    string
	State     memory.StateBag
}

// GetStateBag returns the session's [memory.StateBag] for storing session-scoped provider state.
func (t *Session) GetStateBag() *memory.StateBag { return &t.State }
