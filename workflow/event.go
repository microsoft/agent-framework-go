// Copyright (c) Microsoft. All rights reserved.

package workflow

import "slices"

// Event is implemented by every workflow run event. Data returns the event
// payload.
type Event interface {
	Data() any
}

var _ Event = ExecutorInvokedEvent{}

// ExecutorInvokedEvent is an event triggered when an executor handler is invoked.
type ExecutorInvokedEvent struct {
	ExecutorID string
	Message    any
}

func (e ExecutorInvokedEvent) Data() any {
	return e.Message
}

var _ Event = ExecutorCompletedEvent{}

// ExecutorCompletedEvent is an event triggered when an executor handler completes.
type ExecutorCompletedEvent struct {
	ExecutorID string
	Result     any
}

func (e ExecutorCompletedEvent) Data() any {
	return e.Result
}

var _ Event = ExecutorFailedEvent{}

// ExecutorFailedEvent is an event triggered when an executor handler fails.
type ExecutorFailedEvent struct {
	ExecutorID string
	Error      error
}

func (e ExecutorFailedEvent) Data() any {
	return e.Error
}

// SuperStepStartInfo contains debug information about the [SuperStep] starting to run.
type SuperStepStartInfo struct {
	// The unique identifiers of [Executor] instances that sent messages
	// during the previous [SuperStep].
	SendingExecutors []string

	HasExternalMessages bool
}

var _ Event = SuperStepStartedEvent{}

// SuperStepStartedEvent is an event triggered when a super step starts.
type SuperStepStartedEvent struct {
	StepNumber int
	StartInfo  *SuperStepStartInfo
}

func (e SuperStepStartedEvent) Data() any {
	return e.StartInfo
}

// SuperStepCompletionInfo contains information about a completed super step.
type SuperStepCompletionInfo struct {
	ActivatedExecutors    []string
	InstantiatedExecutors []string
	HasPendingMessages    bool
	HasPendingRequests    bool
	StateUpdated          bool
	CheckpointInfo        *CheckpointInfo
}

var _ Event = SuperStepCompletedEvent{}

// SuperStepCompletedEvent is an event triggered when a super step completes.
type SuperStepCompletedEvent struct {
	StepNumber     int
	CompletionInfo *SuperStepCompletionInfo
}

func (e SuperStepCompletedEvent) Data() any {
	return e.CompletionInfo
}

var _ Event = StartedEvent{}

// StartedEvent is emitted at the beginning of each input → processing → halt
// cycle, immediately before the first [SuperStep] of that cycle runs. It fires
// once per cycle in which there is actual work to process — typically once at
// the start of a run and again whenever new messages or external responses
// arrive after a halt. No event is emitted on cycles that complete without
// work (e.g. timeout-only loop iterations).
type StartedEvent struct {
	Message any
}

func (e StartedEvent) Data() any {
	return e.Message
}

var _ Event = ErrorEvent{}

// ErrorEvent is an event triggered when an error occurs in the workflow.
type ErrorEvent struct {
	Error error

	// Optional SubWorkflowID indicates the sub-workflow where the error occurred.
	SubWorkflowID string
}

func (e ErrorEvent) Data() any {
	return e.Error
}

var _ Event = OutputEvent{}

// OutputEvent is an event triggered when the workflow produces an output.
type OutputEvent struct {
	// ExecutorID is the unique identifier of the executor that yielded this output.
	ExecutorID string
	Output     any
	Tags       []OutputTag
}

func (e OutputEvent) Data() any {
	return e.Output
}

// HasTag reports whether e carries tag.
func (e OutputEvent) HasTag(tag OutputTag) bool {
	return slices.Contains(e.Tags, tag)
}

// IsIntermediate reports whether e carries [OutputTagIntermediate].
func (e OutputEvent) IsIntermediate() bool {
	return e.HasTag(OutputTagIntermediate)
}

var _ Event = RequestHaltEvent{}

// RequestHaltEvent signals that the workflow requested a halt, carrying an
// optional Result payload.
type RequestHaltEvent struct {
	Result any
}

func (e RequestHaltEvent) Data() any {
	return e.Result
}

var _ Event = RequestInfoEvent{}

// RequestInfoEvent is an event containing request information.
type RequestInfoEvent struct {
	Request *ExternalRequest
}

func (e RequestInfoEvent) Data() any {
	return e.Request
}
