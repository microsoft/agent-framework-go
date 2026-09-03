// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/checkpoint"
)

// A Manager for storing and retrieving workflow execution checkpoints.
type Manager interface {
	// LatestCheckpoint returns the most recently committed checkpoint for
	// sessionID, or nil when the session has no checkpoints.
	LatestCheckpoint(ctx context.Context, sessionID string) (*workflow.CheckpointInfo, error)

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
	// mu guards Store (and the per-session caches within it) so a single
	// manager can be shared across concurrent workflow runs — which its
	// session-keyed design implies — without racing on the map or the caches.
	mu    sync.Mutex
	Store map[string]*checkpoint.SessionCache[*checkpoint.Checkpoint]
}

func (s *inMemoryManager) internal() {}

func (s *inMemoryManager) sessionStore(sessionID string) *checkpoint.SessionCache[*checkpoint.Checkpoint] {
	if s.Store == nil {
		s.Store = make(map[string]*checkpoint.SessionCache[*checkpoint.Checkpoint])
	}
	store, ok := s.Store[sessionID]
	if !ok {
		store = &checkpoint.SessionCache[*checkpoint.Checkpoint]{}
		s.Store[sessionID] = store
	}
	return store
}

func (s *inMemoryManager) Commit(_ context.Context, sessionID string, checkpoint *checkpoint.Checkpoint) (workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: sessionID cannot be empty")
	}
	if checkpoint == nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: checkpoint cannot be nil")
	}
	if checkpoint.Parent != nil && checkpoint.Parent.SessionID != sessionID {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: parent sessionID %q does not match sessionID %q", checkpoint.Parent.SessionID, sessionID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	store := s.sessionStore(sessionID)
	return store.Add(sessionID, checkpoint), nil
}

func (s *inMemoryManager) Lookup(_ context.Context, sessionID string, checkpointInfo workflow.CheckpointInfo) (*checkpoint.Checkpoint, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("checkpoint: sessionID cannot be empty")
	}
	if checkpointInfo.SessionID != sessionID {
		return nil, fmt.Errorf("checkpoint: checkpoint sessionID %q does not match sessionID %q", checkpointInfo.SessionID, sessionID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
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

	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *inMemoryManager) LatestCheckpoint(ctx context.Context, sessionID string) (*workflow.CheckpointInfo, error) {
	index, err := s.RetrieveIndex(ctx, sessionID, nil)
	if err != nil || len(index) == 0 {
		return nil, err
	}
	return &index[len(index)-1], nil
}

type jsonManager struct {
	store Store[json.RawMessage]
}

func (s *jsonManager) internal() {}

func (s *jsonManager) Commit(ctx context.Context, sessionID string, checkpoint *checkpoint.Checkpoint) (workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: sessionID cannot be empty")
	}
	if checkpoint == nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: checkpoint cannot be nil")
	}
	if checkpoint.Parent != nil && checkpoint.Parent.SessionID != sessionID {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: parent sessionID %q does not match sessionID %q", checkpoint.Parent.SessionID, sessionID)
	}
	v, err := marshalCheckpoint(checkpoint)
	if err != nil {
		return workflow.CheckpointInfo{}, err
	}

	info, err := s.store.CreateCheckpoint(ctx, sessionID, v, checkpoint.Parent)
	if err != nil {
		return workflow.CheckpointInfo{}, err
	}
	if err := validateManagerCheckpointInfo(sessionID, info); err != nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint: store returned invalid checkpoint info: %w", err)
	}
	return info, nil
}

func (s *jsonManager) Lookup(ctx context.Context, sessionID string, checkpointInfo workflow.CheckpointInfo) (*checkpoint.Checkpoint, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("checkpoint: sessionID cannot be empty")
	}
	if err := validateManagerCheckpointInfo(sessionID, checkpointInfo); err != nil {
		return nil, fmt.Errorf("checkpoint: invalid checkpoint info: %w", err)
	}
	v, err := s.store.RetrieveCheckpoint(ctx, sessionID, checkpointInfo)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve checkpoint with ID %s for session %s: %w", checkpointInfo.CheckpointID, sessionID, err)
	}
	return unmarshalCheckpoint(v, checkpointInfo, sessionID)
}

func (s *jsonManager) RetrieveIndex(ctx context.Context, sessionID string, withParent *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("checkpoint: sessionID cannot be empty")
	}
	if withParent != nil && *withParent != (workflow.CheckpointInfo{}) {
		if err := validateManagerCheckpointInfo(sessionID, *withParent); err != nil {
			return nil, fmt.Errorf("checkpoint: invalid parent checkpoint info: %w", err)
		}
	}
	index, err := s.store.RetrieveIndex(ctx, sessionID, withParent)
	if err != nil {
		return nil, err
	}
	for _, info := range index {
		if err := validateManagerCheckpointInfo(sessionID, info); err != nil {
			return nil, fmt.Errorf("checkpoint: store returned invalid checkpoint info: %w", err)
		}
	}
	return index, nil
}

func (s *jsonManager) LatestCheckpoint(ctx context.Context, sessionID string) (*workflow.CheckpointInfo, error) {
	index, err := s.RetrieveIndex(ctx, sessionID, nil)
	if err != nil || len(index) == 0 {
		return nil, err
	}
	return &index[len(index)-1], nil
}

func validateManagerCheckpointInfo(sessionID string, info workflow.CheckpointInfo) error {
	if info.SessionID == "" {
		return fmt.Errorf("checkpoint sessionID cannot be empty")
	}
	if info.CheckpointID == "" {
		return fmt.Errorf("checkpointID cannot be empty")
	}
	if info.SessionID != sessionID {
		return fmt.Errorf("checkpoint sessionID %q does not match sessionID %q", info.SessionID, sessionID)
	}
	return nil
}

func marshalCheckpoint(cp *checkpoint.Checkpoint) (json.RawMessage, error) {
	v, err := json.Marshal(cp)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize checkpoint: %w", err)
	}
	return v, nil
}

func unmarshalCheckpoint(v json.RawMessage, checkpointInfo workflow.CheckpointInfo, sessionID string) (*checkpoint.Checkpoint, error) {
	var cp checkpoint.Checkpoint
	if err := json.Unmarshal(v, &cp); err != nil {
		return nil, fmt.Errorf("failed to deserialize checkpoint data for checkpoint with ID %s for session %s: %w", checkpointInfo.CheckpointID, sessionID, err)
	}
	return &cp, nil
}
