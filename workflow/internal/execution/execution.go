// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"context"
	"reflect"
	"sync"

	"github.com/microsoft/agent-framework-go/workflow"
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
	mu          sync.RWMutex
	EventRaised []func(context.Context, any, workflow.Event) error
}

func (s *ConcurrentEventSink) AddHandler(handler func(context.Context, any, workflow.Event) error) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EventRaised = append(s.EventRaised, handler)
}

func (s *ConcurrentEventSink) RemoveHandler(handler func(context.Context, any, workflow.Event) error) {
	if handler == nil {
		return
	}
	target := reflect.ValueOf(handler).Pointer()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, current := range s.EventRaised {
		if reflect.ValueOf(current).Pointer() == target {
			s.EventRaised = append(s.EventRaised[:i], s.EventRaised[i+1:]...)
			return
		}
	}
}

func (s *ConcurrentEventSink) HandlerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.EventRaised)
}

func (s *ConcurrentEventSink) Enqueue(ctx context.Context, evt workflow.Event) error {
	s.mu.RLock()
	handlers := append([]func(context.Context, any, workflow.Event) error(nil), s.EventRaised...)
	s.mu.RUnlock()

	for _, handler := range handlers {
		if err := handler(ctx, nil, evt); err != nil {
			return err
		}
	}
	return nil
}

type SuperStepRunner interface {
	Workflow() *workflow.Workflow

	SessionID() string
	StartExecutorID() string
	HasUnservicedRequests() bool
	HasUnprocessedMessages() bool
	RepublishPendingEvents(context.Context) error

	EnqueueResponse(context.Context, *workflow.ExternalResponse) error
	IsValidInputType(context.Context, reflect.Type) bool
	EnqueueMessage(context.Context, any) error

	OutgoingEvents() *ConcurrentEventSink

	RunSuperStep(context.Context) (bool, error)

	RequestEndRun(context.Context) error

	// ResponsePortExecutorID returns the executor that handles responses
	// on the given port, or ("", false) if no such port is registered.
	ResponsePortExecutorID(portID string) (string, bool)
}
