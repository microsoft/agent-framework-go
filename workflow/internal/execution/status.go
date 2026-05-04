// Copyright (c) Microsoft. All rights reserved.

package execution

type RunStatus int

const (
	RunStatusNotStarted RunStatus = iota
	RunStatusIdle
	RunStatusPendingRequests
	RunStatusEnded
	RunStatusRunning
)
