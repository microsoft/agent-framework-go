// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"sync/atomic"
)

type TurnToken struct {
	EmitEvents *bool
}

func (t TurnToken) EmitEventsOr(defaultValue bool) bool {
	if t.EmitEvents != nil {
		return *t.EmitEvents
	}
	return defaultValue
}

type ScopeKey struct {
	ID  ScopeID
	Key string
}

type ScopeID struct {
	Name       string
	ExecutorID string
}

type ProtocolDescriptor struct {
	Accepts []reflect.Type
}

type Workflow struct {
	Name             string
	Description      string
	StartExecutorID  string
	ExecutorBindings map[string]*ExecutorBinding
	Edges            map[string][]Edge
	OutputExecutors  map[string]struct{}
	Ports            map[string]RequestPort

	needsReset         atomic.Bool
	ownerToken         atomic.Value
	ownedAsSubworkflow bool
}

func (w *Workflow) DescribeProtocol() (*ProtocolDescriptor, error) {
	er := w.ExecutorBindings[w.StartExecutorID]
	executor, err := er.CreateInstance("")
	if err != nil {
		return nil, err
	}
	router, err := executor.Router()
	if err != nil {
		return nil, err
	}
	return &ProtocolDescriptor{Accepts: router.IncomingTypes()}, nil
}

func (w *Workflow) HasResettableExecutors() bool {
	for _, er := range w.ExecutorBindings {
		if er.Reset != nil {
			return true
		}
	}
	return false
}

func (w *Workflow) TryReset() bool {
	if !w.HasResettableExecutors() {
		return false
	}
	for _, er := range w.ExecutorBindings {
		if !er.TryReset() {
			return false
		}
	}
	w.needsReset.Store(false)
	return true
}

func (w *Workflow) CheckOwnership(token any) bool {
	return w.ownerToken.Load() == token
}

func (w *Workflow) TakeOwnership(token any, newToken any, subworkflow bool) error {
	// Perform atomic compare-and-swap
	ownerToken := w.ownerToken.Load()
	if ownerToken == nil && token != nil {
		return errors.New("existing ownership token was provided, but the workflow is unowned")
	}
	if w.ownerToken.CompareAndSwap(token, newToken) {
		if ownerToken == nil && w.needsReset.Load() {
			// There is no owner, but the workflow failed to reset on ownership release
			// (because there are shared executors).
			return errors.New("cannot reuse Workflow with shared Executor instances that do not implement IResettableExecutor")
		}
	} else {
		// Someone else owns the workflow
		switch {
		case subworkflow && w.ownedAsSubworkflow:
			return errors.New("cannot use a Workflow as a subworkflow of multiple parent workflows")
		case subworkflow && !w.ownedAsSubworkflow:
			return errors.New("cannot use a running Workflow as a subworkflow")
		case !subworkflow && w.ownedAsSubworkflow:
			return errors.New("cannot directly run a Workflow that is a subworkflow of another workflow")
		case !subworkflow && !w.ownedAsSubworkflow:
			return errors.New("cannot use a Workflow that is already owned by another runner or parent workflow")
		default:
			panic("unreachable")
		}
	}
	// Successfully took ownership (or was already owned by us)
	w.needsReset.Store(true)
	w.ownedAsSubworkflow = subworkflow
	return nil
}

func (w *Workflow) ReleaseOwnership(token any) error {
	ownerToken := w.ownerToken.Load()
	if ownerToken == nil {
		return errors.New("attempting to release ownership of a Workflow that is not owned")
	}
	if !w.ownerToken.CompareAndSwap(token, nil) {
		return errors.New("attempt to release ownership of a Workflow by non-owner")
	}
	w.TryReset()
	return nil
}

func (w *Workflow) ReflectEdges() map[string][]EdgeInfo {
	edgeInfos := make(map[string][]EdgeInfo, len(w.Edges))
	for sourceID, edges := range w.Edges {
		infos := make([]EdgeInfo, 0, len(edges))
		for _, edge := range edges {
			infos = append(infos, NewEdgeInfo(edge))
		}
		edgeInfos[sourceID] = infos
	}
	return edgeInfos
}

func (w *Workflow) ReflectPorts() map[string]RequestPortInfo {
	portInfos := make(map[string]RequestPortInfo, len(w.Ports))
	for id, port := range w.Ports {
		portInfos[id] = NewRequestPortInfo(port)
	}
	return portInfos
}

// Context provides services for an [Executor] during the execution of a workflow.
type Context struct {
	context.Context

	// AddEvent adds an event to the workflow's output queue. These events will be raised to the caller of the workflow at the
	// end of the current [SuperStep].
	AddEvent func(event Event) error

	// SendMessage queues a message to be sent to connected executors. The message will be sent during the next [SuperStep].
	// targetID is an optional identifier of the target executor. If empty, the message is sent to all connected
	// executors. If the target executor is not connected from this executor via an edge, it will still not receive the
	// message
	SendMessage func(targetID string, message any) error

	// YieldOutput adds an output value to the workflow's output queue.
	// These outputs will be bubbled out of the workflow using the [SuperStep].
	//
	// The type of the output message must match one of the output types declared by the [Executor]. By default, the return
	// types of registered message handlers are considered output types, unless otherwise specified using [ExecutorOptions].
	YieldOutput func(output any) error

	// RequestHalt adds a request to "halt" workflow execution at the end of the current [SuperStep].
	RequestHalt func() error

	// ReadState reads a state value from the workflow's state store. If no scope is provided, the executor's
	// default scope is used.
	ReadState func(key string, scope string) (any, error)

	// ReadOrInitState reads or initializes a state value from the workflow's state store.
	// If no scope is provided, the executor's default scope is used.
	ReadOrInitState func(key string, scope string, initFunc func(ctx context.Context, key string, scope string) (any, error)) (any, error)

	// ReadStateKeys reads all state keys within the specified scope.
	// If no scope is provided, the executor's default scope is used.
	ReadStateKeys func(scope string) iter.Seq2[string, error]

	// QueueStateUpdate updates the state of a queue entry identified by the specified key and optional scope.
	// If no scope is provided, the executor's default scope is used.
	QueueStateUpdate func(key string, scope string, value any) error

	// TraceContext returns the trace context associated with the current message about to be processed by the executor, if any.
	TraceContext func() map[string]any

	// ConcurrentRunsEnabled returns whether the current execution environment support concurrent runs against the same workflow instance.
	ConcurrentRunsEnabled func() bool
}

func (ctx *Context) GetContext() context.Context {
	if ctx == nil || ctx.Context == nil {
		return context.Background()
	}
	return ctx.Context
}
