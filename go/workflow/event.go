// Copyright (c) Microsoft. All rights reserved.

package workflow

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

var _ Event = StartedEvent{}

// StartedEvent is an event triggered when the workflow starts.
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
	SourceID string
	Output   any
}

func (e OutputEvent) Data() any {
	return e.Output
}

var _ Event = RequestInfoEvent{}

// RequestInfoEvent is an event containing request information.
type RequestInfoEvent struct {
	Request *ExternalRequest
}

func (e RequestInfoEvent) Data() any {
	return e.Request
}
