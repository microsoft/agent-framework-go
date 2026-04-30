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
	SessionID() string
	GetStatus(ctx context.Context) (RunStatus, error)
	OutgoingEvents() iter.Seq[Event]
	NewEventCount() int
	NewEvents() iter.Seq[Event]
	Resume(ctx context.Context, responses ...*ExternalResponse) (bool, error)
}

type StreamingRun interface {
	SessionID() string
	GetStatus(ctx context.Context) (RunStatus, error)
	SendResponse(ctx context.Context, response *ExternalResponse) error
	SendMessage(ctx context.Context, message any) error

	// WatchStream returns an iterator over workflow events. The iterator
	// blocks at [RunStatusPendingRequests], waiting for a response to be
	// supplied via [SendResponse], and ends only when the run reaches
	// [RunStatusIdle] or [RunStatusEnded] (or its context is canceled).
	WatchStream(ctx context.Context) iter.Seq2[Event, error]

	// WatchUntilHalt returns an iterator over workflow events that ends
	// at the next halt boundary, including [RunStatusPendingRequests]. Use
	// this when the caller wants to observe pending external requests and
	// resume the run later via [SendResponse].
	WatchUntilHalt(ctx context.Context) iter.Seq2[Event, error]

	// ResponsePortExecutorID returns the executor that handles responses
	// on the given [RequestPort], or ("", false) if no such port is registered.
	ResponsePortExecutorID(portID string) (string, bool)

	Cancel()
}
