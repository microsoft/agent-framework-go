// Copyright (c) Microsoft. All rights reserved.

package checkpoint

import (
	"context"

	"github.com/microsoft/agent-framework/go/workflow"
)

type CheckpointingHandle interface {
	Checkpoints() []workflow.CheckpointInfo
	RestoreCheckpoint(context.Context, workflow.CheckpointInfo) error
}

type Manager interface {
	Commit(runID string, checkpoint *Checkpoint) (workflow.CheckpointInfo, error)
	Lookup(runID string) (*Checkpoint, error)
}

type Checkpoint struct {
	StepNumber    int
	WorkflowInfo  WorkflowInfo
	RunnerData    RunnerStateData
	StateData     map[workflow.ScopeKey]workflow.PortableValue
	EdgeStateData map[string]workflow.PortableValue
	Parent        workflow.CheckpointInfo
}

func (c *Checkpoint) IsInitial() bool {
	return c.StepNumber == -1
}

type PortableMessageEnvelope struct {
	MessageType workflow.TypeID
	Message     workflow.PortableValue
	SourceID    string
	TargetID    string
}

type RunnerStateData struct {
	InstantiatedExecutors map[string]struct{}
	QueuedMessages        map[string]PortableMessageEnvelope
	OutstandingRequests   []*workflow.ExternalRequest
}
