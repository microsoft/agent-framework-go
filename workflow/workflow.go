// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"iter"
	"maps"
	"reflect"
	"sync/atomic"

	"github.com/microsoft/agent-framework-go/workflow/internal/observability"
)

// TurnToken is a control message that advances a workflow turn.
type TurnToken struct {
	// EmitEvents overrides the workflow's default event-emission behavior for
	// this turn when set.
	EmitEvents *bool
}

// EmitEventsOr returns the token's event-emission override, or defaultValue if
// the token does not specify one.
func (t TurnToken) EmitEventsOr(defaultValue bool) bool {
	if t.EmitEvents != nil {
		return *t.EmitEvents
	}
	return defaultValue
}

// ScopeKey identifies a single state value within a workflow state scope.
type ScopeKey struct {
	// ID identifies the state scope that owns the key.
	ID ScopeID

	// Key identifies the value within the scope.
	Key string
}

// Equal reports whether s and other identify the same state value.
func (s ScopeKey) Equal(other ScopeKey) bool {
	return s.ID.Equal(other.ID) && s.Key == other.Key
}

// Hash writes a stable hash representation of s into h.
func (s ScopeKey) Hash(h *maphash.Hash) {
	s.ID.Hash(h)
	h.WriteString(s.Key)
}

// ScopeID is a unique identifier for a workflow state scope. If a scope name is
// not provided, it references the default scope private to the executor.
// Otherwise, regardless of the executor ID, it references a shared scope with
// the specified name.
type ScopeID struct {
	_ [0]func() // disallow ==
	// ScopeName identifies a shared scope when set.
	ScopeName string

	// ExecutorID identifies the executor that owns a private default scope.
	ExecutorID string
}

// Equal reports whether s and other refer to the same workflow state scope.
func (s ScopeID) Equal(other ScopeID) bool {
	if other.ScopeName == "" && s.ScopeName == "" {
		return other.ExecutorID == s.ExecutorID
	}
	if other.ScopeName != "" && s.ScopeName != "" {
		return other.ScopeName == s.ScopeName
	}
	return false
}

// Hash writes a stable hash representation of s into h.
func (s ScopeID) Hash(h *maphash.Hash) {
	if s.ScopeName == "" {
		h.WriteString(s.ExecutorID)
	} else {
		h.WriteString(s.ScopeName)
	}
}

// scopeIDJSON is the JSON representation of ScopeID, omitting the
// non-serializable equality-guard field.
type scopeIDJSON struct {
	ScopeName  string `json:",omitempty"`
	ExecutorID string `json:",omitempty"`
}

// MarshalJSON implements [json.Marshaler] for ScopeID.
func (s ScopeID) MarshalJSON() ([]byte, error) {
	return json.Marshal(scopeIDJSON{
		ScopeName:  s.ScopeName,
		ExecutorID: s.ExecutorID,
	})
}

// UnmarshalJSON implements [json.Unmarshaler] for ScopeID.
func (s *ScopeID) UnmarshalJSON(data []byte) error {
	var v scopeIDJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	s.ScopeName = v.ScopeName
	s.ExecutorID = v.ExecutorID
	return nil
}

// ProtocolDescriptor describes the message protocol accepted, yielded, and sent
// by an executor or workflow.
type ProtocolDescriptor struct {
	// Accepts lists the message types accepted by the described protocol.
	Accepts []reflect.Type

	// Yields lists the message types the described protocol can yield as output.
	Yields []reflect.Type

	// Sends lists the message types the described protocol can send to connected
	// executors. Workflow descriptors leave this empty.
	Sends []reflect.Type

	// AcceptsAll reports whether the described protocol has a catch-all handler.
	AcceptsAll bool
}

// Workflow is an executable graph of bound executors, edges, outputs, and
// request ports.
type Workflow struct {
	// Name is an optional human-readable workflow name.
	Name string

	// Description is optional human-readable workflow detail.
	Description string

	// StartExecutorID is the executor ID that receives the initial input.
	StartExecutorID string

	// ExecutorBindings contains the workflow's executor bindings keyed by ID.
	ExecutorBindings map[string]ExecutorBinding

	// Edges contains outgoing edges keyed by source executor ID.
	Edges map[string][]Edge

	// OutputExecutors contains the executor IDs whose yielded outputs are exposed
	// as workflow outputs.
	OutputExecutors map[string]struct{}

	// Ports contains request ports exposed by the workflow keyed by port ID.
	Ports map[string]RequestPort

	needsReset atomic.Bool
	ownerToken atomic.Pointer[workflowOwner]
	telemetry  *observability.Context
}

type workflowOwner struct {
	token       any
	subworkflow bool
}

func ownershipTokenIdentity(token any) (reflect.Type, uintptr, bool) {
	if token == nil {
		return nil, 0, false
	}
	value := reflect.ValueOf(token)
	switch value.Kind() {
	case reflect.Chan, reflect.Map, reflect.Pointer, reflect.UnsafePointer:
		if value.IsNil() {
			return nil, 0, false
		}
		return value.Type(), value.Pointer(), true
	default:
		return nil, 0, false
	}
}

func isOwnershipToken(token any) bool {
	_, _, ok := ownershipTokenIdentity(token)
	return ok
}

func sameOwnershipToken(left any, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftType, leftPtr, leftOK := ownershipTokenIdentity(left)
	rightType, rightPtr, rightOK := ownershipTokenIdentity(right)
	return leftOK && rightOK && leftType == rightType && leftPtr == rightPtr
}

// DescribeProtocol returns the protocol accepted by the workflow's start
// executor and yielded by its output executors.
func (w *Workflow) DescribeProtocol() (*ProtocolDescriptor, error) {
	er := w.ExecutorBindings[w.StartExecutorID]
	if _, ok := w.ExecutorBindings[w.StartExecutorID]; !ok {
		return nil, fmt.Errorf("workflow start executor %q has no registered binding", w.StartExecutorID)
	}
	executor, err := er.CreateInstance("")
	if err != nil {
		return nil, err
	}
	inputProtocol, err := executor.describeProtocol()
	if err != nil {
		return nil, err
	}

	yields := make([]reflect.Type, 0)
	for executorID := range w.OutputExecutors {
		binding, ok := w.ExecutorBindings[executorID]
		if !ok {
			return nil, fmt.Errorf("workflow output executor %q has no registered binding", executorID)
		}
		outputExecutor, err := binding.CreateInstance("")
		if err != nil {
			return nil, err
		}
		outputProtocol, err := outputExecutor.describeProtocol()
		if err != nil {
			return nil, err
		}
		yields = append(yields, outputProtocol.Yields...)
	}

	return &ProtocolDescriptor{
		Accepts:    inputProtocol.Accepts,
		Yields:     yields,
		Sends:      nil,
		AcceptsAll: inputProtocol.AcceptsAll,
	}, nil
}

// HasResettableExecutors reports whether any executor binding can reset shared
// resources between workflow runs.
func (w *Workflow) HasResettableExecutors() bool {
	for _, er := range w.ExecutorBindings {
		if er.ResetFunc != nil {
			return true
		}
	}
	return false
}

// ContextWithTelemetry returns ctx annotated with the workflow's telemetry
// context.
func (w *Workflow) ContextWithTelemetry(ctx context.Context) context.Context {
	if w == nil {
		return observability.ContextWithTelemetry(ctx, nil)
	}
	return observability.ContextWithTelemetry(ctx, w.telemetry)
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

// TryReset attempts to reset all shared executor bindings that require reset
// support before workflow reuse.
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

// CheckOwnership reports whether token currently owns the workflow. A nil token
// matches only an unowned workflow. Non-nil tokens are compared by pointer-like
// identity, matching .NET's reference-token ownership model.
func (w *Workflow) CheckOwnership(token any) bool {
	owner := w.ownerToken.Load()
	if owner == nil {
		return token == nil
	}
	return sameOwnershipToken(owner.token, token)
}

// TakeOwnership transfers workflow ownership from token to newToken. The
// subworkflow flag records whether the workflow is being owned by a parent
// workflow rather than by a direct runner. newToken must be a non-nil
// pointer-like value so ownership can be compared by identity.
func (w *Workflow) TakeOwnership(token any, newToken any, subworkflow bool) error {
	if newToken == nil {
		return errors.New("new ownership token cannot be nil")
	}
	if !isOwnershipToken(newToken) {
		return errors.New("new ownership token must be a non-nil pointer-like value")
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

		if sameOwnershipToken(owner.token, token) || sameOwnershipToken(owner.token, newToken) {
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
			return errors.New("cannot directly run a Workflow that is a subworkflow of another workflow")
		case !subworkflow && !owner.subworkflow:
			return errors.New("cannot use a Workflow that is already owned by another runner or parent workflow")
		default:
			panic("unreachable")
		}
	}

	// Successfully took ownership (or was already owned by us)
	w.needsReset.Store(w.HasResettableExecutors())
	return nil
}

// ReleaseOwnership releases workflow ownership held by token and attempts to
// reset shared executor state. token is compared by pointer-like identity.
func (w *Workflow) ReleaseOwnership(token any) error {
	return w.ReleaseOwnershipTo(token, nil)
}

// ReleaseOwnershipTo releases workflow ownership held by token, restores
// targetToken as the owner when non-nil, and attempts to reset shared executor
// state. Non-nil tokens are compared by pointer-like identity.
func (w *Workflow) ReleaseOwnershipTo(token any, targetToken any) error {
	if targetToken != nil && !isOwnershipToken(targetToken) {
		return errors.New("target ownership token must be a non-nil pointer-like value")
	}
	for {
		owner := w.ownerToken.Load()
		if owner == nil {
			return errors.New("attempting to release ownership of a Workflow that is not owned")
		}
		if !sameOwnershipToken(owner.token, token) {
			return errors.New("attempt to release ownership of a Workflow by non-owner")
		}
		var nextOwner *workflowOwner
		if targetToken != nil {
			nextOwner = &workflowOwner{token: targetToken, subworkflow: owner.subworkflow}
		}
		if w.ownerToken.CompareAndSwap(owner, nextOwner) {
			break
		}
	}

	w.TryReset()
	return nil
}

// ReflectEdges returns workflow edge metadata keyed by source executor ID.
func (w *Workflow) ReflectEdges() map[string][]EdgeInfo {
	edgeInfos := make(map[string][]EdgeInfo, len(w.Edges))
	for sourceID, edges := range w.Edges {
		infos := make([]EdgeInfo, 0, len(edges))
		for _, edge := range edges {
			infos = append(infos, newEdgeInfo(edge))
		}
		edgeInfos[sourceID] = infos
	}
	return edgeInfos
}

// ReflectPorts returns workflow request port metadata keyed by port ID.
func (w *Workflow) ReflectPorts() map[string]RequestPortInfo {
	portInfos := make(map[string]RequestPortInfo, len(w.Ports))
	for id, port := range w.Ports {
		portInfos[id] = NewRequestPortInfo(port)
	}
	return portInfos
}

// ReflectExecutors returns a copy of the workflow's executor bindings keyed
// by ID. Modifying the returned map does not affect the workflow.
func (w *Workflow) ReflectExecutors() map[string]ExecutorBinding {
	out := make(map[string]ExecutorBinding, len(w.ExecutorBindings))
	maps.Copy(out, w.ExecutorBindings)
	return out
}

// Context provides services for an [Executor] during the execution of a workflow.
type Context struct {
	// Context carries cancellation, deadlines, and request-scoped values for the
	// current executor invocation.
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
	// types of registered message handlers are considered output types, unless otherwise specified using [ExecutorSpec].
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

	// ConcurrentRunsEnabled reports whether the current execution environment
	// supports concurrent runs against the same workflow instance.
	ConcurrentRunsEnabled bool
}

// GetContext returns the underlying context or [context.Background] when ctx or
// its embedded context is nil.
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
