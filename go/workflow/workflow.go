// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"sync/atomic"
)

// Context provides services for an [Executor] during the execution of a workflow.
type Context interface {
	// AddEvent adds an event to the workflow's output queue. These events will be raised to the caller of the workflow at the
	// end of the current [SuperStep].
	AddEvent(ctx context.Context, event Event) error

	// SendMessage queues a message to be sent to connected executors. The message will be sent during the next [SuperStep].
	// targetID is an optional identifier of the target executor. If empty, the message is sent to all connected
	// executors. If the target executor is not connected from this executor via an edge, it will still not receive the
	// message
	SendMessage(ctx context.Context, message any, targetID string) error

	// YieldOutput adds an output value to the workflow's output queue.
	// These outputs will be bubbled out of the workflow using the [SuperStep].
	//
	// The type of the output message must match one of the output types declared by the [Executor]. By default, the return
	// types of registered message handlers are considered output types, unless otherwise specified using [ExecutorOptions].
	YieldOutput(ctx context.Context, output any) error

	// RequestHalt adds a request to "halt" workflow execution at the end of the current [SuperStep].
	RequestHalt(ctx context.Context) error

	// ReadState reads a state value from the workflow's state store. If no scope is provided, the executor's
	// default scope is used.
	ReadState(ctx context.Context, key string, scope string) (any, error)

	// ReadOrInitState reads or initializes a state value from the workflow's state store.
	// If no scope is provided, the executor's default scope is used.
	ReadOrInitState(ctx context.Context, key string, scope string, initFunc func(ctx context.Context, key string, scope string) (any, error)) (any, error)

	// ReadStateKeys reads all state keys within the specified scope.
	// If no scope is provided, the executor's default scope is used.
	ReadStateKeys(ctx context.Context, scope string) iter.Seq2[string, error]

	// QueueStateUpdate updates the state of a queue entry identified by the specified key and optional scope.
	// If no scope is provided, the executor's default scope is used.
	QueueStateUpdate(ctx context.Context, key string, scope string, value any) error

	// TraceContext returns the trace context associated with the current message about to be processed by the executor, if any.
	TraceContext() map[string]any

	// ConcurrentRunsEnabled returns whether the current execution environment support concurrent runs against the same workflow instance.
	ConcurrentRunsEnabled() bool
}

type Workflow struct {
	registrations   map[string]*executorRegistration
	Edges           map[string][]Edge
	Ports           map[string]RequestPort
	StartExecutorId string
	Name            string
	Description     string

	needsReset         atomic.Bool
	ownerToken         atomic.Value
	ownedAsSubworkflow bool
}

func (w *Workflow) AllowConcurrent() bool {
	for _, er := range w.registrations {
		if !er.supportsConcurrent() {
			return false
		}
	}
	return true
}

func (w *Workflow) NonConcurrentExecutorIds() iter.Seq[string] {
	return func(yield func(string) bool) {
		for id, er := range w.registrations {
			if !er.supportsConcurrent() {
				if !yield(id) {
					return
				}
			}
		}
	}
}

func (w *Workflow) Resettable() bool {
	for _, er := range w.registrations {
		if er.isUnresettableSharedInstance() {
			return false
		}
	}
	return true
}

func (w *Workflow) DescribeProtocol() iter.Seq[reflect.Type] {
	er := w.registrations[w.StartExecutorId]
	router, err := er.newExecutor("").Router()
	if err != nil {
		return nil
	}
	return router.IncomingTypes()
}

func (w *Workflow) checkOwnership(token any) bool {
	return w.ownerToken.Load() == token
}

func (w *Workflow) takeOwnership(token any, newToken any, subworkflow bool) error {
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

func (w *Workflow) releaseOwnership(token any) error {
	ownerToken := w.ownerToken.Load()
	if ownerToken == nil {
		return errors.New("attempting to release ownership of a Workflow that is not owned")
	}
	if !w.ownerToken.CompareAndSwap(token, nil) {
		return errors.New("attempt to release ownership of a Workflow by non-owner")
	}
	// Try to reset the workflow
	if !w.Resettable() {
		return nil
	}
	for _, er := range w.registrations {
		if !er.tryReset() {
			return nil
		}
	}
	w.needsReset.Store(false)
	return nil
}

type WorkflowBuilder struct {
	startExecutorId string
	name            string
	description     string

	err error

	edgeCount                int
	executors                map[string]Executor
	edges                    map[string][]Edge
	conditionlessConnections []EdgeConnection
	inputPorts               map[string]RequestPort
	outputExecutors          map[string]struct{}
}

func NewWorkflowBuilder(start Executor) *WorkflowBuilder {
	bld := &WorkflowBuilder{
		startExecutorId: start.ID(),
		executors:       make(map[string]Executor),
		edges:           make(map[string][]Edge),
		inputPorts:      make(map[string]RequestPort),
		outputExecutors: make(map[string]struct{}),
	}
	return bld
}

func (wb *WorkflowBuilder) WithName(name string) *WorkflowBuilder {
	if wb.err != nil {
		return wb
	}
	wb.name = name
	return wb
}

func (wb *WorkflowBuilder) WithDescription(description string) *WorkflowBuilder {
	if wb.err != nil {
		return wb
	}
	wb.description = description
	return wb
}

func (wb *WorkflowBuilder) WithOutputFrom(executors ...Executor) *WorkflowBuilder {
	if wb.err != nil {
		return wb
	}
	for _, e := range executors {
		if !wb.track(e) {
			return wb
		}
		wb.outputExecutors[e.ID()] = struct{}{}
	}
	return wb
}

func (wb *WorkflowBuilder) AddEdge(source Executor, target Executor, idempotent bool, condition func(any) bool) *WorkflowBuilder {
	if wb.err != nil {
		return wb
	}
	conn := EdgeConnection{
		SourceIDs: []string{source.ID()},
		SinkIDs:   []string{target.ID()},
	}
	if condition == nil && slices.ContainsFunc(wb.conditionlessConnections, func(c EdgeConnection) bool {
		return conn.Equal(c)
	}) {
		if idempotent {
			return wb
		}
		wb.err = fmt.Errorf("an edge from '%s' to '%s' already exists without a condition", source.ID(), target.ID())
		return wb
	}
	if !wb.track(source) || !wb.track(target) {
		return wb
	}
	wb.edges[source.ID()] = append(wb.edges[source.ID()], Edge{
		Connection: conn,
		Condition:  condition,
		Index:      wb.edgeIdx(),
	})
	wb.conditionlessConnections = append(wb.conditionlessConnections, conn)
	return wb
}

func (wb *WorkflowBuilder) AddFanOutEdge(source Executor, targets []Executor, partitioner func(any, int, []int) bool) *WorkflowBuilder {
	if wb.err != nil {
		return wb
	}
	if !wb.track(source) {
		return wb
	}
	sinkIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		if !wb.track(target) {
			return wb
		}
		sinkIDs = append(sinkIDs, target.ID())
	}
	conn := EdgeConnection{
		SourceIDs: []string{source.ID()},
		SinkIDs:   sinkIDs,
	}
	wb.edges[source.ID()] = append(wb.edges[source.ID()], Edge{
		Connection: conn,
		Index:      wb.edgeIdx(),
	})
	return wb
}

func (wb *WorkflowBuilder) AddFanInEdge(target Executor, sources []Executor) *WorkflowBuilder {
	if wb.err != nil {
		return wb
	}
	if !wb.track(target) {
		return wb
	}
	sourceIDs := make([]string, 0, len(sources))
	for _, source := range sources {
		if !wb.track(source) {
			return wb
		}
		sourceIDs = append(sourceIDs, source.ID())
	}
	edge := Edge{
		Connection: EdgeConnection{
			SourceIDs: sourceIDs,
			SinkIDs:   []string{target.ID()},
		},
		Index: wb.edgeIdx(),
	}
	for _, id := range sourceIDs {
		wb.edges[id] = append(wb.edges[id], edge)
	}
	return wb
}

func (wb *WorkflowBuilder) Build() (*Workflow, error) {
	if wb.err != nil {
		return nil, wb.err
	}
	var wf Workflow
	wf.StartExecutorId = wb.startExecutorId
	wf.Name = wb.name
	wf.Description = wb.description
	wf.Edges = wb.edges
	wf.Ports = wb.inputPorts
	wf.registrations = make(map[string]*executorRegistration)
	return &wf, nil
}

func (wb *WorkflowBuilder) edgeIdx() int {
	wb.edgeCount++
	return wb.edgeCount
}

func (wb *WorkflowBuilder) track(e Executor) bool {
	if wb.err != nil {
		return false
	}
	if prev, exists := wb.executors[e.ID()]; exists && prev != e {
		wb.err = errors.New("cannot add multiple different instances with the same ID: " + e.ID())
		return false
	}
	wb.executors[e.ID()] = e
	if port, ok := e.(*requestPortExecutor); ok {
		wb.inputPorts[port.ID()] = port.port
	}
	return true
}
