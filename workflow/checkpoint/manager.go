// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
	internalcheckpoint "github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

// A Manager for storing and retrieving workflow execution checkpoints.
type Manager interface {
	internal()
}

// NewInMemoryManager creates a new instance of the [Manager]
// that uses in-memory storage for checkpoint data.
func NewInMemoryManager() Manager {
	return &inMemoryManager{}
}

// NewJSONManager creates a new instance of the [Manager]
// that uses JSON serialization for checkpoint data.
func NewJSONManager(store Store[json.RawMessage]) Manager {
	if store == nil {
		panic("checkpoint: store cannot be nil")
	}
	return &jsonManager{store: store}
}

type inMemoryManager struct {
	Store map[string]*internalcheckpoint.SessionCache[*internalcheckpoint.Checkpoint]
}

func (s *inMemoryManager) internal() {}

func (s *inMemoryManager) sessionStore(sessionID string) *internalcheckpoint.SessionCache[*internalcheckpoint.Checkpoint] {
	if s.Store == nil {
		s.Store = make(map[string]*internalcheckpoint.SessionCache[*internalcheckpoint.Checkpoint])
	}
	store, ok := s.Store[sessionID]
	if !ok {
		store = &internalcheckpoint.SessionCache[*internalcheckpoint.Checkpoint]{}
		s.Store[sessionID] = store
	}
	return store
}

func (s *inMemoryManager) Commit(_ context.Context, sessionID string, checkpoint *internalcheckpoint.Checkpoint) (workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: sessionID cannot be empty")
	}
	if checkpoint == nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: checkpoint cannot be nil")
	}
	if checkpoint.Parent != nil && checkpoint.Parent.SessionID != sessionID {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: parent sessionID %q does not match sessionID %q", checkpoint.Parent.SessionID, sessionID)
	}

	store := s.sessionStore(sessionID)
	return store.Add(sessionID, checkpoint), nil
}

func (s *inMemoryManager) Lookup(_ context.Context, sessionID string, checkpointInfo workflow.CheckpointInfo) (*internalcheckpoint.Checkpoint, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("checkpoint: sessionID cannot be empty")
	}
	if checkpointInfo.SessionID != sessionID {
		return nil, fmt.Errorf("checkpoint: checkpoint sessionID %q does not match sessionID %q", checkpointInfo.SessionID, sessionID)
	}

	store := s.sessionStore(sessionID)
	v, ok := store.Get(checkpointInfo)
	if !ok {
		return nil, fmt.Errorf("could not retrieve checkpoint with ID %s for session %s", checkpointInfo.CheckpointID, sessionID)
	}
	return v, nil
}

func (s *inMemoryManager) RetrieveIndex(_ context.Context, sessionID string, withParent *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("checkpoint: sessionID cannot be empty")
	}
	if withParent != nil && *withParent != (workflow.CheckpointInfo{}) && withParent.SessionID != sessionID {
		return nil, fmt.Errorf("checkpoint: parent sessionID %q does not match sessionID %q", withParent.SessionID, sessionID)
	}

	store := s.sessionStore(sessionID)
	if withParent == nil {
		return slices.Clone(store.CheckpointIndex), nil
	}

	var result []workflow.CheckpointInfo
	for _, info := range store.CheckpointIndex {
		checkpoint, ok := store.Get(info)
		if !ok || checkpoint.Parent == nil || *checkpoint.Parent != *withParent {
			continue
		}
		result = append(result, info)
	}
	return result, nil
}

type jsonManager struct {
	store Store[json.RawMessage]
}

func (s *jsonManager) internal() {}

func (s *jsonManager) Commit(ctx context.Context, sessionID string, checkpoint *internalcheckpoint.Checkpoint) (workflow.CheckpointInfo, error) {
	if checkpoint == nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: checkpoint cannot be nil")
	}
	v, err := json.Marshal(checkpoint)
	if err != nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("failed to serialize checkpoint: %w", err)
	}

	return s.store.CreateCheckpoint(ctx, sessionID, v, checkpoint.Parent)
}

func (s *jsonManager) Lookup(ctx context.Context, sessionID string, checkpointInfo workflow.CheckpointInfo) (*internalcheckpoint.Checkpoint, error) {
	v, ok := s.store.RetrieveCheckpoint(ctx, sessionID, checkpointInfo)
	if ok != nil {
		return nil, fmt.Errorf("could not retrieve checkpoint with ID %s for session %s: %w", checkpointInfo.CheckpointID, sessionID, ok)
	}
	var checkpoint internalcheckpoint.Checkpoint
	if err := json.Unmarshal(v, &checkpoint); err != nil {
		return nil, fmt.Errorf("failed to deserialize checkpoint data for checkpoint with ID %s for session %s: %w", checkpointInfo.CheckpointID, sessionID, err)
	}
	return &checkpoint, nil
}

func (s *jsonManager) RetrieveIndex(ctx context.Context, sessionID string, withParent *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error) {
	return s.store.RetrieveIndex(ctx, sessionID, withParent)
}
