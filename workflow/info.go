// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"strings"

	"github.com/google/uuid"
)

// CheckpointInfo identifies a persisted workflow checkpoint.
//
// Checkpoint managers use this value to store, retrieve, index, and resume
// checkpoints for a workflow session.
type CheckpointInfo struct {
	// SessionID is the workflow session that owns the checkpoint.
	SessionID string `json:"sessionId"`

	// CheckpointID is unique within the session and identifies one stored
	// checkpoint record.
	CheckpointID string `json:"checkpointId"`
}

// NewCheckpointInfo creates checkpoint metadata for sessionID.
//
// The generated checkpoint ID is a UUID string without hyphen separators so it
// can be used safely in checkpoint indexes and file names.
func NewCheckpointInfo(sessionID string) CheckpointInfo {
	return CheckpointInfo{
		SessionID:    sessionID,
		CheckpointID: strings.ReplaceAll(uuid.NewString(), "-", ""),
	}
}

// EdgeInfo is a serializable description of a workflow edge.
//
// It records the edge connection and metadata that can be reflected or
// serialized, while representing condition and assigner callbacks only by their
// presence.
type EdgeInfo struct {
	// Connection describes the edge endpoints and connection shape.
	Connection EdgeConnection

	// Label is the optional label associated with the edge.
	Label string

	// HasCondition reports whether the edge has a condition callback.
	HasCondition bool

	// HasAssigner reports whether the edge has a fan-out assigner callback.
	HasAssigner bool
}

// newEdgeInfo creates the reflected edge metadata for edge.
func newEdgeInfo(edge Edge) EdgeInfo {
	return EdgeInfo{
		Connection:   edge.Connection,
		Label:        edge.Label,
		HasCondition: edge.Condition != nil,
		HasAssigner:  edge.Assigner != nil,
	}
}

// Match reports whether other has the same reflected edge metadata.
//
// Callback functions are compared by presence only; the function values
// themselves are not comparable and are not represented in [EdgeInfo].
func (e *EdgeInfo) Match(other Edge) bool {
	return e.Connection.Equal(other.Connection) &&
		e.Label == other.Label &&
		e.HasCondition == (other.Condition != nil) &&
		e.HasAssigner == (other.Assigner != nil)
}

// RequestPortInfo describes a workflow request port.
//
// Request and response types are stored as [TypeID] values so external requests
// and responses can be validated after serialization or across package
// boundaries.
type RequestPortInfo struct {
	// PortID is the request port's workflow-unique identifier.
	PortID string

	// RequestType is the type accepted by the request port.
	RequestType TypeID

	// ResponseType is the type returned through the request port.
	ResponseType TypeID
}

// NewRequestPortInfo creates a serializable descriptor for port.
func NewRequestPortInfo(port RequestPort) RequestPortInfo {
	return RequestPortInfo{
		PortID:       port.ID,
		RequestType:  NewTypeID(port.Request),
		ResponseType: NewTypeID(port.Response),
	}
}
