// Copyright (c) Microsoft. All rights reserved.

package workflow

// CheckpointManager provides checkpoint management for workflow execution.
// Use [NewCheckpointManager] to create an instance backed by a [CheckpointStore],
// or [NewInMemoryCheckpointManager] for a development/testing store.
//
// This mirrors the .NET CheckpointManager pattern: the manager owns
// serialization and delegates storage to the pluggable store.
type CheckpointManager struct {
	store CheckpointStore
}

// NewCheckpointManager creates a CheckpointManager backed by the given store.
// The store is responsible only for durable persistence of opaque JSON data;
// the framework handles serialization of internal checkpoint structures.
func NewCheckpointManager(store CheckpointStore) *CheckpointManager {
	return &CheckpointManager{store: store}
}

// Store returns the underlying [CheckpointStore].
func (m *CheckpointManager) Store() CheckpointStore {
	return m.store
}
