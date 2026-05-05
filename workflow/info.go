// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"github.com/google/uuid"
)

type CheckpointInfo struct {
	SessionID    string
	CheckpointID string
}

func NewCheckpointInfo(sessionID string) CheckpointInfo {
	return CheckpointInfo{
		SessionID:    sessionID,
		CheckpointID: uuid.NewString(),
	}
}

type EdgeInfo struct {
	Connection   EdgeConnection
	Label        string
	HasCondition bool
	HasAssigner  bool
}

func NewEdgeInfo(edge Edge) EdgeInfo {
	return EdgeInfo{
		Connection:   edge.Connection,
		Label:        edge.Label,
		HasCondition: edge.Condition != nil,
		HasAssigner:  edge.Assigner != nil,
	}
}

func (e *EdgeInfo) Match(other Edge) bool {
	return e.Connection.Equal(other.Connection) &&
		e.Label == other.Label &&
		e.HasCondition == (other.Condition != nil) &&
		e.HasAssigner == (other.Assigner != nil)
}

// RequestPortInfo contains information about a request port, including its
// request and response types.
type RequestPortInfo struct {
	PortID       string
	RequestType  TypeID
	ResponseType TypeID
}

func NewRequestPortInfo(port RequestPort) RequestPortInfo {
	return RequestPortInfo{
		PortID:       port.ID,
		RequestType:  NewTypeID(port.Request),
		ResponseType: NewTypeID(port.Response),
	}
}
