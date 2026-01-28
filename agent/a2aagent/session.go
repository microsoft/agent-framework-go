// Copyright (c) Microsoft. All rights reserved.

package a2aagent

import "encoding/json"

// Session represents a session identified by a service-managed identifier.
type Session struct {
	ContextID string
	TaskID    string
}

func (t *Session) MarshalBinary() (data []byte, err error) {
	return json.Marshal(t)
}
