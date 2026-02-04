// Copyright (c) Microsoft. All rights reserved.

package a2aagent

// Session represents a session identified by a service-managed identifier.
type Session struct {
	ContextID string
	TaskID    string
}

func (t *Session) IsAgentSession() {}
