// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"github.com/google/uuid"
)

type CheckpointInfo struct {
	RunID        string
	CheckpointID string
}

func NewCheckpointInfo(runID string) CheckpointInfo {
	return CheckpointInfo{
		RunID:        runID,
		CheckpointID: uuid.NewString(),
	}
}

type EdgeInfo struct {
	Connection   EdgeConnection
	HasCondition bool
	HasAssigner  bool
}

func NewEdgeInfo(edge Edge) EdgeInfo {
	return EdgeInfo{
		Connection:   edge.Connection,
		HasCondition: edge.Condition != nil,
		HasAssigner:  edge.Assigner != nil,
	}
}

func (e *EdgeInfo) Match(other Edge) bool {
	return e.Connection.Equal(other.Connection) &&
		e.HasCondition == (other.Condition != nil) &&
		e.HasAssigner == (other.Assigner != nil)
}

// RequestPortInfo contains information about an input port, including its input and output types.
type RequestPortInfo struct {
	ID           string
	RequestType  TypeID
	ResponseType TypeID
}

func NewRequestPortInfo(port RequestPort) RequestPortInfo {
	return RequestPortInfo{
		ID:           port.ID,
		RequestType:  NewTypeID(port.Request),
		ResponseType: NewTypeID(port.Response),
	}
}
