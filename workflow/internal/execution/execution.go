// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"reflect"

	"github.com/microsoft/agent-framework/go/workflow"
)

type Mode int

const (
	ModeOffThread Mode = iota
	ModeLockstep
	ModeSubworkflow
)

type EventSink interface {
	Enqueue(context.Context, workflow.Event) error
}

var _ EventSink = (*ConcurrentEventSink)(nil)

type ConcurrentEventSink struct {
	EventRaised []func(context.Context, any, workflow.Event) error
}

func (s *ConcurrentEventSink) Enqueue(ctx context.Context, evt workflow.Event) error {
	for _, handler := range s.EventRaised {
		if err := handler(ctx, nil, evt); err != nil {
			return err
		}
	}
	return nil
}

type SuperStepRunner interface {
	RunID() string
	StartExecutorID() string
	HasUnservicedRequests() bool
	HasUnprocessedMessages() bool

	EnqueueResponse(context.Context, *workflow.ExternalResponse) error
	IsValidInputType(context.Context, reflect.Type) bool
	EnqueueMessage(context.Context, any) error

	OutgoingEvents() *ConcurrentEventSink

	RunSuperStep(context.Context) (bool, error)

	RequestEndRun(context.Context) error
}
