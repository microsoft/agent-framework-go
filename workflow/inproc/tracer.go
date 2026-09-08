// Copyright (c) Microsoft. All rights reserved.

package inproc

import (
	"slices"

	"github.com/microsoft/agent-framework-go/internal/concurrent"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/internal/execution"
)

var _ execution.StepTracer = (*stepTracer)(nil)

// stepTracer tracks workflow execution progress within a single step.
type stepTracer struct {
	stepNumber     int
	stateUpdated   bool
	checkpointInfo *workflow.CheckpointInfo

	instantiated concurrent.Map[string, string]
	activated    concurrent.Map[string, string]
}

// StepNumber returns the current step number (0-indexed).
func (t *stepTracer) StepNumber() int {
	return t.stepNumber - 1
}

// StateUpdated returns true if state was updated in this step.
func (t *stepTracer) StateUpdated() bool {
	return t.stateUpdated
}

// Checkpoint returns the checkpoint info created in this step, if any.
func (t *stepTracer) Checkpoint() *workflow.CheckpointInfo {
	return t.checkpointInfo
}

// TraceInstantiated records that an executor was instantiated.
func (t *stepTracer) TraceInstantiated(executorID string) {
	t.instantiated.Store(executorID, executorID)
}

// TraceActivated records that an executor was activated.
func (t *stepTracer) TraceActivated(executorID string) {
	t.activated.Store(executorID, executorID)
}

// TraceStatePublished records that state was published in this step.
func (t *stepTracer) TraceStatePublished() {
	t.stateUpdated = true
}

// TraceCheckpointCreated records that a checkpoint was created.
func (t *stepTracer) TraceCheckpointCreated(cp workflow.CheckpointInfo) {
	t.checkpointInfo = &cp
}

// Reload resets the tracer to the specified step number.
func (t *stepTracer) Reload(lastStepNumber int) {
	t.stepNumber = lastStepNumber + 1
}

// Advance advances to the next step and returns a SuperStepStartedEvent.
func (t *stepTracer) Advance(step *execution.StepContext) workflow.SuperStepStartedEvent {
	t.stepNumber++

	// Clear per-step tracking
	t.activated.Clear()
	t.instantiated.Clear()
	t.stateUpdated = false
	t.checkpointInfo = nil

	// Collect the executors that sent this step's messages, and whether any
	// message came from an external source. StepContext is keyed by the message
	// TARGET, so the sender/external signal lives on each envelope's SourceID
	// (empty SourceID == external), not on the map key.
	sendingExecutors := make([]string, 0)
	hasExternalMessages := false

	for _, target := range step.Keys() {
		for envelope := range step.MessagesFor(target).All() {
			if envelope.IsExternal() {
				hasExternalMessages = true
			} else {
				sendingExecutors = append(sendingExecutors, envelope.SourceID)
			}
		}
	}
	slices.Sort(sendingExecutors)
	sendingExecutors = slices.Compact(sendingExecutors)

	return workflow.SuperStepStartedEvent{
		StepNumber: t.StepNumber(),
		StartInfo: &workflow.SuperStepStartInfo{
			SendingExecutors:    sendingExecutors,
			HasExternalMessages: hasExternalMessages,
		},
	}
}

// Complete generates a SuperStepCompletedEvent for the current step.
func (t *stepTracer) Complete(nextStepHasActions, hasPendingRequests bool) workflow.SuperStepCompletedEvent {
	return workflow.SuperStepCompletedEvent{
		StepNumber: t.StepNumber(),
		CompletionInfo: &workflow.SuperStepCompletionInfo{
			ActivatedExecutors:    sortedTrackedExecutorIDs(&t.activated),
			InstantiatedExecutors: sortedTrackedExecutorIDs(&t.instantiated),
			HasPendingMessages:    nextStepHasActions,
			HasPendingRequests:    hasPendingRequests,
			StateUpdated:          t.StateUpdated(),
			CheckpointInfo:        t.checkpointInfo,
		},
	}
}

func sortedTrackedExecutorIDs(executors *concurrent.Map[string, string]) []string {
	ids := slices.Collect(executors.Keys())
	slices.Sort(ids)
	return ids
}
