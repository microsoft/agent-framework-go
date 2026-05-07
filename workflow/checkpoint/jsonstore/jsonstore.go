// Copyright (c) Microsoft. All rights reserved.

// Package jsonstore provides a JSON file-based [workflow.CheckpointStore]
// implementation that persists checkpoint data to the local file system.
//
// Each session gets a directory under the configured root path. Checkpoint
// data is stored as individual JSON files and a session index tracks the
// ordering.
//
// This store is suitable for local development, CLI tools, and lightweight
// deployments. For production use with concurrent access or cloud storage,
// implement [workflow.CheckpointStore] with an appropriate backend.
package jsonstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/workflow"
)

// Store is a JSON file-based implementation of [workflow.CheckpointStore].
type Store struct {
	rootDir string
	mu      sync.Mutex
}

// New creates a Store rooted at the given directory path.
// The directory is created if it does not exist.
func New(rootDir string) (*Store, error) {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("jsonstore: create root dir: %w", err)
	}
	return &Store{rootDir: rootDir}, nil
}

func (s *Store) sessionDir(sessionID string) string {
	return filepath.Join(s.rootDir, sessionID)
}

func (s *Store) indexPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), "index.json")
}

func (s *Store) checkpointPath(sessionID, checkpointID string) string {
	return filepath.Join(s.sessionDir(sessionID), checkpointID+".json")
}

func (s *Store) readIndex(sessionID string) ([]workflow.CheckpointInfo, error) {
	data, err := os.ReadFile(s.indexPath(sessionID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var index []workflow.CheckpointInfo
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return index, nil
}

func (s *Store) writeIndex(sessionID string, index []workflow.CheckpointInfo) error {
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return os.WriteFile(s.indexPath(sessionID), data, 0o644)
}

// CreateCheckpoint implements [workflow.CheckpointStore].
func (s *Store) CreateCheckpoint(sessionID string, data json.RawMessage, _ *workflow.CheckpointInfo) (workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return workflow.CheckpointInfo{}, fmt.Errorf("jsonstore: sessionID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.sessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("jsonstore: create session dir: %w", err)
	}

	info := workflow.CheckpointInfo{
		SessionID:    sessionID,
		CheckpointID: uuid.NewString(),
	}

	if err := os.WriteFile(s.checkpointPath(sessionID, info.CheckpointID), data, 0o644); err != nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("jsonstore: write checkpoint: %w", err)
	}

	index, err := s.readIndex(sessionID)
	if err != nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("jsonstore: read index: %w", err)
	}
	index = append(index, info)
	if err := s.writeIndex(sessionID, index); err != nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("jsonstore: write index: %w", err)
	}

	return info, nil
}

// RetrieveCheckpoint implements [workflow.CheckpointStore].
func (s *Store) RetrieveCheckpoint(sessionID string, info workflow.CheckpointInfo) (json.RawMessage, error) {
	data, err := os.ReadFile(s.checkpointPath(sessionID, info.CheckpointID))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("jsonstore: checkpoint %s not found for session %s", info.CheckpointID, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("jsonstore: read checkpoint: %w", err)
	}
	return data, nil
}

// RetrieveIndex implements [workflow.CheckpointStore].
func (s *Store) RetrieveIndex(sessionID string, _ *workflow.CheckpointInfo) ([]workflow.CheckpointInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	index, err := s.readIndex(sessionID)
	if err != nil {
		return nil, fmt.Errorf("jsonstore: read index: %w", err)
	}
	return index, nil
}
