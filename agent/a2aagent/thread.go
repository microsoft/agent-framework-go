// Copyright (c) Microsoft. All rights reserved.

package a2aagent

import "encoding/json"

// Thread represents a thread identified by a service-managed identifier.
type Thread struct {
	ContextID string
	TaskID    string
}

func (t *Thread) MarshalBinary() (data []byte, err error) {
	return json.Marshal(t)
}
