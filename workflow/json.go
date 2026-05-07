// Copyright (c) Microsoft. All rights reserved.

package workflow

import "encoding/json"

// scopeIDJSON is the JSON representation of ScopeID, omitting the
// non-serializable equality-guard field.
type scopeIDJSON struct {
	ScopeName  string `json:",omitempty"`
	ExecutorID string `json:",omitempty"`
}

// MarshalJSON implements [json.Marshaler] for ScopeID.
func (s ScopeID) MarshalJSON() ([]byte, error) {
	return json.Marshal(scopeIDJSON{
		ScopeName:  s.ScopeName,
		ExecutorID: s.ExecutorID,
	})
}

// UnmarshalJSON implements [json.Unmarshaler] for ScopeID.
func (s *ScopeID) UnmarshalJSON(data []byte) error {
	var v scopeIDJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	s.ScopeName = v.ScopeName
	s.ExecutorID = v.ExecutorID
	return nil
}
