// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"hash/maphash"
	"iter"
	"maps"
	"reflect"
	"sync/atomic"

	"github.com/microsoft/agent-framework-go/workflow/internal/observability"
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

func (s ScopeKey) Equal(other ScopeKey) bool {
	return s.ID.Equal(other.ID) && s.Key == other.Key
}

func (s ScopeKey) Hash(h *maphash.Hash) {
	s.ID.Hash(h)
	h.WriteString(s.Key)
}

// ScopeID is a unique identifier for a scope within an executor. If a scope name is not provided, it references the
// default scope private to the executor. Otherwise, regardless of the executorId, it references a shared
// scope with the specified name.
type ScopeID struct {
	_          [0]func() // disallow ==
	ScopeName  string
	ExecutorID string
}

func (s ScopeID) Equal(other ScopeID) bool {
	if other.ScopeName == "" && s.ScopeName == "" {
		return other.ExecutorID == s.ExecutorID
	}
	if other.ScopeName != "" && s.ScopeName != "" {
		return other.ScopeName == s.ScopeName
	}
	return false
}

func (s ScopeID) Hash(h *maphash.Hash) {
	if s.ScopeName == "" {
		h.WriteString(s.ExecutorID)
	} else {
		h.WriteString(s.ScopeName)
	}
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

	needsReset atomic.Bool
	ownerToken atomic.Pointer[workflowOwner]
	telemetry  *observability.Context
}

type workflowOwner struct {
	token       any
	subworkflow bool
}

func (w *Workflow) DescribeProtocol() (*ProtocolDescriptor, error) {
	er := w.ExecutorBindings[w.StartExecutorID]
	if er == nil {
		return nil, fmt.Errorf("workflow start executor %q has no registered binding", w.StartExecutorID)
	}
	executor, err := er.CreateInstance("")
	if err != nil {
		return nil, err
	}
	router, err := executor.router()
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

func (w *Workflow) ContextWithTelemetry(ctx context.Context) context.Context {
	if w == nil {
		return observability.ContextWithTelemetry(ctx, nil)
	}
	return observability.ContextWithTelemetry(ctx, w.telemetry)
}

func (w *Workflow) hasSharedExecutors() bool {
	for _, er := range w.ExecutorBindings {
		if er.IsSharedInstance {
			return true
		}
	}
	return false
}

// AllowConcurrent reports whether every bound executor in the workflow
// supports cross-run shared execution.
func (w *Workflow) AllowConcurrent() bool {
	for _, er := range w.ExecutorBindings {
		if !er.SupportsConcurrentSharedExecution {
			return false
		}
	}
	return true
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
	owner := w.ownerToken.Load()
	if owner == nil {
		return token == nil
	}
	return owner.token == token
}

func (w *Workflow) TakeOwnership(token any, newToken any, subworkflow bool) error {
	if newToken == nil {
		return errors.New("new ownership token cannot be nil")
	}

	newOwner := &workflowOwner{token: newToken, subworkflow: subworkflow}
	for {
		owner := w.ownerToken.Load()
		if owner == nil {
			if token != nil {
				return errors.New("existing ownership token was provided, but the workflow is unowned")
			}
			if w.needsReset.Load() {
				// There is no owner, but the workflow failed to reset on ownership release
				// (because there are shared executors).
				return errors.New("cannot reuse Workflow with shared Executor instances that do not implement IResettableExecutor")
			}
			if w.ownerToken.CompareAndSwap(nil, newOwner) {
				break
			}
			continue
		}

		if owner.token == token || owner.token == newToken {
			if w.ownerToken.CompareAndSwap(owner, newOwner) {
				break
			}
			continue
		}

		// Someone else owns the workflow
		switch {
		case subworkflow && owner.subworkflow:
			return errors.New("cannot use a Workflow as a subworkflow of multiple parent workflows")
		case subworkflow && !owner.subworkflow:
			return errors.New("cannot use a running Workflow as a subworkflow")
		case !subworkflow && owner.subworkflow:
			return errors.New("cannot directly run a Workflow that is a subworkflow")
		case !subworkflow && !owner.subworkflow:
			return errors.New("cannot use a Workflow that is already owned by another runner or parent workflow")
		default:
			panic("unreachable")
		}
	}

	// Successfully took ownership (or was already owned by us)
	w.needsReset.Store(w.hasSharedExecutors())
	return nil
}

func (w *Workflow) ReleaseOwnership(token any) error {
	for {
		owner := w.ownerToken.Load()
		if owner == nil {
			return errors.New("attempting to release ownership of a Workflow that is not owned")
		}
		if owner.token != token {
			return errors.New("attempt to release ownership of a Workflow by non-owner")
		}
		if w.ownerToken.CompareAndSwap(owner, nil) {
			break
		}
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

// ReflectExecutors returns a copy of the workflow's executor bindings keyed
// by ID. Modifying the returned map does not affect the workflow.
func (w *Workflow) ReflectExecutors() map[string]*ExecutorBinding {
	out := make(map[string]*ExecutorBinding, len(w.ExecutorBindings))
	maps.Copy(out, w.ExecutorBindings)
	return out
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

	// PostRequest raises an [ExternalRequest] from the current executor.
	// The request becomes a [RequestInfoEvent] in the workflow event stream,
	// and the matching [ExternalResponse] (sent later by the caller via the
	// run handle) is delivered back to this executor as a regular message of
	// type *[ExternalResponse]. The executor is responsible for registering
	// a handler for *[ExternalResponse] via its [RouteBuilder] and extracting
	// the typed payload via [ExternalResponse.Data] and [PortableValue.As].
	PostRequest func(request *ExternalRequest) error

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
	ConcurrentRunsEnabled bool
}

func (ctx *Context) GetContext() context.Context {
	if ctx == nil || ctx.Context == nil {
		return context.Background()
	}
	return ctx.Context
}

func (ctx *Context) telemetry() *observability.Context {
	return observability.FromContext(ctx.GetContext())
}

func (ctx *Context) traceContextStrings() map[string]string {
	if ctx == nil || ctx.TraceContext == nil {
		return nil
	}
	traceContext := ctx.TraceContext()
	if len(traceContext) == 0 {
		return nil
	}
	result := make(map[string]string, len(traceContext))
	for key, value := range traceContext {
		if str, ok := value.(string); ok {
			result[key] = str
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
