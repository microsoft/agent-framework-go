// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"encoding/json"
	"hash/maphash"

	"github.com/microsoft/agent-framework-go/internal/hashmap"
	"github.com/microsoft/agent-framework-go/workflow"
)

// scopeKeyHasherImpl implements hashmap.Hasher for workflow.ScopeKey.
// This is duplicated here to avoid an import cycle with execution.
type scopeKeyHasherImpl struct{}

var scopeKeyHasherInstance hashmap.Hasher[workflow.ScopeKey] = scopeKeyHasherImpl{}

func (scopeKeyHasherImpl) Hash(s workflow.ScopeKey) uint64 {
	var h maphash.Hash
	s.Hash(&h)
	return h.Sum64()
}

func (scopeKeyHasherImpl) Equal(a, b workflow.ScopeKey) bool {
	return a.Equal(b)
}

// scopeKeyEntry is the JSON-friendly representation of a ScopeKey→PortableValue pair.
type scopeKeyEntry struct {
	Key   workflow.ScopeKey
	Value workflow.PortableValue
}

// checkpointJSON is the JSON representation of a Checkpoint.
type checkpointJSON struct {
	StepNumber    int
	WorkflowInfo  WorkflowInfo
	RunnerData    RunnerStateData
	StateData     []scopeKeyEntry
	EdgeStateData map[string]workflow.PortableValue
	Parent        workflow.CheckpointInfo
}

// MarshalJSON implements [json.Marshaler] for Checkpoint.
func (c *Checkpoint) MarshalJSON() ([]byte, error) {
	var entries []scopeKeyEntry
	for key, value := range c.StateData.All() {
		entries = append(entries, scopeKeyEntry{Key: key, Value: value})
	}

	return json.Marshal(checkpointJSON{
		StepNumber:    c.StepNumber,
		WorkflowInfo:  c.WorkflowInfo,
		RunnerData:    c.RunnerData,
		StateData:     entries,
		EdgeStateData: c.EdgeStateData,
		Parent:        c.Parent,
	})
}

// UnmarshalJSON implements [json.Unmarshaler] for Checkpoint.
func (c *Checkpoint) UnmarshalJSON(data []byte) error {
	var v checkpointJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	stateData := hashmap.NewMap[workflow.ScopeKey, workflow.PortableValue](scopeKeyHasherInstance)
	for _, entry := range v.StateData {
		stateData.Set(entry.Key, entry.Value)
	}

	c.StepNumber = v.StepNumber
	c.WorkflowInfo = v.WorkflowInfo
	c.RunnerData = v.RunnerData
	c.StateData = *stateData
	c.EdgeStateData = v.EdgeStateData
	c.Parent = v.Parent
	return nil
}
