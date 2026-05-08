// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/microsoft/agent-framework-go/internal/hashmap"
	"github.com/microsoft/agent-framework-go/workflow"
)

type CheckpointingHandle interface {
	IsCheckpointingEnabled() bool
	Checkpoints() []workflow.CheckpointInfo
	RestoreCheckpoint(context.Context, workflow.CheckpointInfo) error
}

type Manager interface {
	Commit(sessionID string, checkpoint *Checkpoint) (workflow.CheckpointInfo, error)
	Lookup(sessionID string, checkpointInfo workflow.CheckpointInfo) (*Checkpoint, error)
	RetrieveIndex(sessionID string) ([]workflow.CheckpointInfo, error)
}

type InMemoryManager struct {
	mu       sync.RWMutex
	sessions map[string]*sessionCheckpointCache
}

type sessionCheckpointCache struct {
	index       []workflow.CheckpointInfo
	checkpoints map[string]*Checkpoint
}

func NewInMemoryManager() *InMemoryManager {
	return &InMemoryManager{
		sessions: make(map[string]*sessionCheckpointCache),
	}
}

func (m *InMemoryManager) Commit(sessionID string, checkpoint *Checkpoint) (workflow.CheckpointInfo, error) {
	if sessionID == "" {
		return workflow.CheckpointInfo{}, fmt.Errorf("sessionID cannot be empty")
	}
	if checkpoint == nil {
		return workflow.CheckpointInfo{}, fmt.Errorf("checkpoint cannot be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	session := m.ensureSessionLocked(sessionID)
	var info workflow.CheckpointInfo
	for {
		info = workflow.NewCheckpointInfo(sessionID)
		if _, exists := session.checkpoints[info.CheckpointID]; !exists {
			break
		}
	}
	session.checkpoints[info.CheckpointID] = checkpoint
	session.index = append(session.index, info)
	return info, nil
}

func (m *InMemoryManager) Lookup(sessionID string, checkpointInfo workflow.CheckpointInfo) (*Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("could not retrieve checkpoint with id %s for session %s", checkpointInfo.CheckpointID, sessionID)
	}
	cp, ok := session.checkpoints[checkpointInfo.CheckpointID]
	if !ok {
		return nil, fmt.Errorf("could not retrieve checkpoint with id %s for session %s", checkpointInfo.CheckpointID, sessionID)
	}
	return cp, nil
}

func (m *InMemoryManager) RetrieveIndex(sessionID string) ([]workflow.CheckpointInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	return slices.Clone(session.index), nil
}

func (m *InMemoryManager) ensureSessionLocked(sessionID string) *sessionCheckpointCache {
	if session, ok := m.sessions[sessionID]; ok {
		return session
	}
	session := &sessionCheckpointCache{
		checkpoints: make(map[string]*Checkpoint),
	}
	m.sessions[sessionID] = session
	return session
}

type Checkpoint struct {
	StepNumber    int
	WorkflowInfo  WorkflowInfo
	RunnerData    RunnerStateData
	StateData     hashmap.Map[workflow.ScopeKey, workflow.PortableValue]
	EdgeStateData map[string]workflow.PortableValue
	Parent        workflow.CheckpointInfo
}

func (c *Checkpoint) IsInitial() bool {
	return c.StepNumber == -1
}

type PortableMessageEnvelope struct {
	MessageType  workflow.TypeID
	Message      workflow.PortableValue
	SourceID     string
	TargetID     string
	TraceContext map[string]string
}

type RunnerStateData struct {
	InstantiatedExecutors map[string]struct{}
	QueuedMessages        map[string][]*PortableMessageEnvelope
	OutstandingRequests   []*workflow.ExternalRequest
	RequestOwners         map[string]string
	ResponsePortOwners    map[string]string
}
