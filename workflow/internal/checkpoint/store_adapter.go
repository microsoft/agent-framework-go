// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework-go/workflow"
)

// StoreAdapter implements [Manager] by delegating to a [workflow.CheckpointStore].
// It serializes/deserializes [Checkpoint] values as JSON.
type StoreAdapter struct {
	store workflow.CheckpointStore
}

// NewStoreAdapter creates a [Manager] backed by the given [workflow.CheckpointStore].
func NewStoreAdapter(store workflow.CheckpointStore) *StoreAdapter {
	return &StoreAdapter{store: store}
}

func (a *StoreAdapter) Commit(sessionID string, cp *Checkpoint) (workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return workflow.CheckpointInfo{}, fmt.Errorf("sessionID cannot be empty")
	}
	if cp == nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint cannot be nil")
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("failed to marshal checkpoint: %w", err)
	}
	var parent *workflow.CheckpointInfo
	if cp.Parent != (workflow.CheckpointInfo{}) {
		parent = &cp.Parent
	}
	return a.store.CreateCheckpoint(sessionID, data, parent)
}

func (a *StoreAdapter) Lookup(sessionID string, info workflow.CheckpointInfo) (*Checkpoint, error) {
	data, err := a.store.RetrieveCheckpoint(sessionID, info)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve checkpoint: %w", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoint: %w", err)
	}
	return &cp, nil
}

func (a *StoreAdapter) RetrieveIndex(sessionID string) ([]workflow.CheckpointInfo, error) {
	return a.store.RetrieveIndex(sessionID, nil)
}
