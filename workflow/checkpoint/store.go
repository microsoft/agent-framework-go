// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"context"

	"github.com/microsoft/agent-framework-go/workflow"
)

// Store defines the interface for persisting and retrieving workflow
// checkpoint data. Implementations receive checkpoint data as values of type T
// and are responsible only for durable storage.
//
// The framework serialises internal checkpoint state before calling the store,
// so store implementations never need to understand the checkpoint structure.
type Store[T any] interface {
	// CreateCheckpoint persists a checkpoint and returns its identifying info.
	// parent is the info of the preceding checkpoint, if any.
	CreateCheckpoint(ctx context.Context, sessionID string, data T, parent *workflow.CheckpointInfo) (workflow.CheckpointInfo, error)

	// RetrieveCheckpoint loads previously saved checkpoint data.
	RetrieveCheckpoint(ctx context.Context, sessionID string, info workflow.CheckpointInfo) (T, error)

	// RetrieveIndex returns the ordered index of checkpoint identifiers for a
	// session. If withParent is non-nil only checkpoints whose parent matches
	// are returned.
	RetrieveIndex(ctx context.Context, sessionID string, withParent *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error)
}
