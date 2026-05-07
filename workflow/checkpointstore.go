// Copyright (c) Microsoft. All rights reserved.

package workflow

import "encoding/json"

// CheckpointStore defines the interface for persisting and retrieving workflow
// checkpoint data. Implementations receive checkpoint data as [json.RawMessage]
// values and are responsible only for durable storage.
//
// This matches the .NET ICheckpointStore<JsonElement> pattern: the framework
// serialises internal checkpoint state to JSON before calling the store, so
// store implementations never need to understand the checkpoint structure.
type CheckpointStore interface {
	// CreateCheckpoint persists a checkpoint and returns its identifying info.
	// parent is the info of the preceding checkpoint, if any.
	CreateCheckpoint(sessionID string, data json.RawMessage, parent *CheckpointInfo) (CheckpointInfo, error)

	// RetrieveCheckpoint loads previously saved checkpoint data.
	RetrieveCheckpoint(sessionID string, info CheckpointInfo) (json.RawMessage, error)

	// RetrieveIndex returns the ordered index of checkpoint identifiers for a
	// session. If withParent is non-nil only checkpoints whose parent matches
	// are returned.
	RetrieveIndex(sessionID string, withParent *CheckpointInfo) ([]CheckpointInfo, error)
}
