// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"

	"github.com/microsoft/agent-framework/go/workflow"
)

type SuperStateRunner interface {
	RunID() string
	StartExecutorID() string
	HasUnservicedRequests() bool
	HasUnprocessedMessages() bool
	EnqueueResponse(ctx context.Context, response workflow.ExternalResponse) error
	RunSuperStep(ctx context.Context) (bool, error)
	RequestEndRun(ctx context.Context) error
}
