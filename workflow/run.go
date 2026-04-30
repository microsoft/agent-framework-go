// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"iter"
)

type RunStatus int

const (
	RunStatusNotStarted RunStatus = iota
	RunStatusIdle
	RunStatusPendingRequests
	RunStatusEnded
	RunStatusRunning
)

type Run interface {
	IsCheckpointingEnabled() bool
	Checkpoints() []CheckpointInfo
	LastCheckpoint() (CheckpointInfo, bool)
	RestoreCheckpoint(ctx context.Context, checkpointInfo CheckpointInfo) error
	SessionID() string
	GetStatus(ctx context.Context) (RunStatus, error)

	OutgoingEvents() iter.Seq[Event]
	NewEventCount() int
	NewEvents() iter.Seq[Event]
	Resume(ctx context.Context, messages ...any) (bool, error)
	Close(ctx context.Context) error
}

type StreamingRun interface {
	IsCheckpointingEnabled() bool
	Checkpoints() []CheckpointInfo
	LastCheckpoint() (CheckpointInfo, bool)
	RestoreCheckpoint(ctx context.Context, checkpointInfo CheckpointInfo) error
	SessionID() string
	GetStatus(ctx context.Context) (RunStatus, error)

	SendResponse(ctx context.Context, response *ExternalResponse) error

	// SendMessage sends a message to the workflow. If the message type is not a
	// valid input type for the workflow, the returned error wraps
	// [ErrInvalidInputType].
	SendMessage(ctx context.Context, message any) error

	// WatchStream returns an iterator over workflow events. The iterator
	// blocks at [RunStatusPendingRequests], waiting for a response to be
	// supplied via [SendResponse], and ends only when the run reaches
	// [RunStatusIdle] or [RunStatusEnded] (or its context is canceled).
	WatchStream(ctx context.Context) iter.Seq2[Event, error]

	CancelRun() error
	Close(ctx context.Context) error
}

// ExecutionEnvironment defines an environment for running, streaming, and
// resuming workflows, with optional checkpointing support.
type ExecutionEnvironment interface {
	// IsCheckpointingEnabled reports whether checkpointing is configured for
	// this environment.
	IsCheckpointingEnabled() bool

	// RunStreaming starts a streaming run of the workflow and sends the provided
	// initial messages. When no messages are provided, the starting executor will
	// not be invoked until an input message is received.
	RunStreaming(ctx context.Context, wf *Workflow, sessionID string, msgs ...any) (StreamingRun, error)

	// ResumeStreaming resumes a streaming workflow run from a checkpoint.
	ResumeStreaming(ctx context.Context, wf *Workflow, fromCheckpoint CheckpointInfo) (StreamingRun, error)

	// Run starts a non-streaming run of the workflow and runs it until its
	// first halt.
	Run(ctx context.Context, wf *Workflow, sessionID string, msgs ...any) (Run, error)

	// Resume resumes a non-streaming workflow run from a checkpoint and
	// runs it until its first halt.
	Resume(ctx context.Context, wf *Workflow, fromCheckpoint CheckpointInfo) (Run, error)
}
