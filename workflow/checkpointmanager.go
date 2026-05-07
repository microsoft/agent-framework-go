// Copyright (c) Microsoft. All rights reserved.

package workflow

import "encoding/json"

// CheckpointManager provides checkpoint management for workflow execution.
// Use [NewCheckpointManager] to create an instance backed by a [CheckpointStore],
// or [NewInMemoryCheckpointManager] for a development/testing store.
type CheckpointManager struct {
	store    CheckpointStore[json.RawMessage]
	internal any  // cached internal checkpoint.Manager, set by WithCheckpointing
	inMemory bool // true when backed by an in-memory store
}

// SetInternal sets an internal manager implementation to avoid creating
// a new adapter on each WithCheckpointing call.
func (m *CheckpointManager) SetInternal(mgr any) {
	m.internal = mgr
}

// IsInMemory reports whether this manager is backed by an in-memory store.
func (m *CheckpointManager) IsInMemory() bool {
	return m.inMemory
}

// NewCheckpointManager creates a CheckpointManager backed by the given store.
// The store is responsible only for durable persistence of opaque JSON data;
// the framework handles serialization of internal checkpoint structures.
//
// Panics if store is nil.
func NewCheckpointManager(store CheckpointStore[json.RawMessage]) *CheckpointManager {
	if store == nil {
		panic("workflow: CheckpointStore must not be nil")
	}
	return &CheckpointManager{store: store}
}

// Store returns the underlying [CheckpointStore].
func (m *CheckpointManager) Store() CheckpointStore[json.RawMessage] {
	return m.store
}

// Internal returns the internal manager if one was set, or nil.
// This is used by the execution environment to avoid JSON round-trips
// for in-memory checkpoint managers.
func (m *CheckpointManager) Internal() any {
	return m.internal
}
