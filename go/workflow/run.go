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
	RunID() string
	GetStatus(ctx context.Context) (RunStatus, error)
	OutgoingEvents() iter.Seq[Event]
	NewEventCount() int
	NewEvents() iter.Seq[Event]
	Resume(ctx context.Context, responses ...*ExternalResponse) (bool, error)
}

type StreamingRun interface {
	RunID() string
	GetStatus(ctx context.Context) (RunStatus, error)
	SendResponse(ctx context.Context, response *ExternalResponse) error
	SendMessage(ctx context.Context, message any) error
	WatchStream(ctx context.Context) iter.Seq2[Event, error]
	Cancel()
}
