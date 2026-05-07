// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// InMemoryCheckpointStore is an in-memory implementation of [CheckpointStore]
// suitable for development and testing. Checkpoints are not persisted beyond
// the lifetime of the process.
type InMemoryCheckpointStore struct {
	mu       sync.RWMutex
	sessions map[string]*sessionCache
}

type sessionCache struct {
	index       []indexEntry
	checkpoints map[string]json.RawMessage
}

type indexEntry struct {
	Info   CheckpointInfo
	Parent *CheckpointInfo
}

// NewInMemoryCheckpointStore creates a new in-memory checkpoint store.
func NewInMemoryCheckpointStore() *InMemoryCheckpointStore {
	return &InMemoryCheckpointStore{
		sessions: make(map[string]*sessionCache),
	}
}

// NewInMemoryCheckpointManager creates a [CheckpointManager] backed by an
// in-memory store. This is a convenience equivalent to
// NewCheckpointManager(NewInMemoryCheckpointStore()).
func NewInMemoryCheckpointManager() *CheckpointManager {
	return NewCheckpointManager(NewInMemoryCheckpointStore())
}

func (s *InMemoryCheckpointStore) ensureSession(sessionID string) *sessionCache {
	if sess, ok := s.sessions[sessionID]; ok {
		return sess
	}
	sess := &sessionCache{
		checkpoints: make(map[string]json.RawMessage),
	}
	s.sessions[sessionID] = sess
	return sess
}

// CreateCheckpoint implements [CheckpointStore].
func (s *InMemoryCheckpointStore) CreateCheckpoint(_ context.Context, sessionID string, data json.RawMessage, parent *CheckpointInfo) (CheckpointInfo, error) {
	if sessionID == "" {
		return CheckpointInfo{}, fmt.Errorf("sessionID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess := s.ensureSession(sessionID)
	info := CheckpointInfo{
		SessionID:    sessionID,
		CheckpointID: uuid.NewString(),
	}
	sess.checkpoints[info.CheckpointID] = append(json.RawMessage(nil), data...)
	sess.index = append(sess.index, indexEntry{Info: info, Parent: parent})
	return info, nil
}

// RetrieveCheckpoint implements [CheckpointStore].
func (s *InMemoryCheckpointStore) RetrieveCheckpoint(_ context.Context, sessionID string, info CheckpointInfo) (json.RawMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("could not retrieve checkpoint with id %s for session %s", info.CheckpointID, sessionID)
	}
	data, ok := sess.checkpoints[info.CheckpointID]
	if !ok {
		return nil, fmt.Errorf("could not retrieve checkpoint with id %s for session %s", info.CheckpointID, sessionID)
	}
	return data, nil
}

// RetrieveIndex implements [CheckpointStore].
func (s *InMemoryCheckpointStore) RetrieveIndex(_ context.Context, sessionID string, withParent *CheckpointInfo) ([]CheckpointInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, nil
	}

	var result []CheckpointInfo
	for _, entry := range sess.index {
		if withParent != nil {
			if entry.Parent == nil || *entry.Parent != *withParent {
				continue
			}
		}
		result = append(result, entry.Info)
	}
	return result, nil
}
