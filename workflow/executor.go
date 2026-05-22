// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/microsoft/agent-framework-go/workflow/internal/observability"
)

// ExecutorSpec configures an [Executor].
//
// The zero value is an empty spec. An [Executor] must have at least one
// non-nil route or lifecycle callback before it can build routes. Use
// [ExecutorSpec.Extend] to add behavior from reusable helpers.
type ExecutorSpec struct {
	// If true, the result of a message handler that returns a value will not be
	// sent as a message from the executor.
	DisableAutoSendMessageHandlerResultObject bool

	// If true, the result of a message handler that returns a value will not be
	// yielded as workflow output from the executor.
	DisableAutoYieldOutputHandlerResultObject bool

	// ConfigureProtocol configures message handlers and declared send/yield
	// message types on the supplied builder.
	// It may return the same builder or a replacement builder.
	ConfigureProtocol func(builder *ProtocolBuilder) (*ProtocolBuilder, error)

	// Initialize is called when the executor instance is created for a run.
	Initialize func(ctx *Context) error

	// Reset clears any executor-local cached state before an instance is reused.
	Reset func() error

	// OnCheckpoint is called before workflow state is checkpointed.
	OnCheckpoint func(ctx *Context) error

	// OnCheckpointRestored is called after workflow state is restored.
	OnCheckpointRestored func(ctx *Context) error

	// OnMessageDeliveryStarting is invoked once per superstep, before any
	// messages are delivered to the executor. It is given a context bound to
	// this executor with no per-message trace context.
	OnMessageDeliveryStarting func(ctx *Context) error

	// OnMessageDeliveryFinished is invoked once per superstep, after all
	// messages have been delivered to the executor (regardless of whether
	// individual deliveries succeeded). It is given a context bound to this
	// executor with no per-message trace context.
	OnMessageDeliveryFinished func(ctx *Context) error
}

// Extend adds spec to s.
//
// Existing protocol configuration and lifecycle hooks run before hooks from spec.
// Most hooks stop on the first error. OnMessageDeliveryFinished runs every hook
// and returns the first error encountered.
//
// Runtime policy fields are combined conservatively: disabling automatic send
// or yield in either spec disables it in the receiver.
func (s *ExecutorSpec) Extend(spec ExecutorSpec) {
	if s == nil {
		panic("workflow: cannot extend nil ExecutorSpec")
	}

	s.DisableAutoSendMessageHandlerResultObject = s.DisableAutoSendMessageHandlerResultObject || spec.DisableAutoSendMessageHandlerResultObject
	s.DisableAutoYieldOutputHandlerResultObject = s.DisableAutoYieldOutputHandlerResultObject || spec.DisableAutoYieldOutputHandlerResultObject
	s.ConfigureProtocol = extendProtocol(s.ConfigureProtocol, spec.ConfigureProtocol)
	s.Initialize = extendContextHook(s.Initialize, spec.Initialize)
	s.Reset = extendResetHook(s.Reset, spec.Reset)
	s.OnCheckpoint = extendContextHook(s.OnCheckpoint, spec.OnCheckpoint)
	s.OnCheckpointRestored = extendContextHook(s.OnCheckpointRestored, spec.OnCheckpointRestored)
	s.OnMessageDeliveryStarting = extendContextHook(s.OnMessageDeliveryStarting, spec.OnMessageDeliveryStarting)
	s.OnMessageDeliveryFinished = extendFinishedHook(s.OnMessageDeliveryFinished, spec.OnMessageDeliveryFinished)
}

func appendUniqueTypes(dst []reflect.Type, src ...reflect.Type) []reflect.Type {
	for _, typ := range src {
		if typ == nil || slices.Contains(dst, typ) {
			continue
		}
		dst = append(dst, typ)
	}
	return dst
}

func extendProtocol(first, second func(*ProtocolBuilder) (*ProtocolBuilder, error)) func(*ProtocolBuilder) (*ProtocolBuilder, error) {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(builder *ProtocolBuilder) (*ProtocolBuilder, error) {
		builder, err := first(builder)
		if err != nil {
			return nil, err
		}
		return second(builder)
	}
}

func extendContextHook(first, second func(*Context) error) func(*Context) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(ctx *Context) error {
		if err := first(ctx); err != nil {
			return err
		}
		return second(ctx)
	}
}

func extendResetHook(first, second func() error) func() error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func() error {
		if err := first(); err != nil {
			return err
		}
		return second()
	}
}

func extendFinishedHook(first, second func(*Context) error) func(*Context) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(ctx *Context) error {
		firstErr := first(ctx)
		if err := second(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}
}

func (s ExecutorSpec) isConfigured() bool {
	return s.ConfigureProtocol != nil ||
		s.Initialize != nil ||
		s.Reset != nil ||
		s.OnCheckpoint != nil ||
		s.OnCheckpointRestored != nil ||
		s.OnMessageDeliveryStarting != nil ||
		s.OnMessageDeliveryFinished != nil
}

// Executor is a runnable workflow node.
//
// Spec defines the executor's runtime behavior, routes, and lifecycle hooks.
// Executors cache their route table after first use.
type Executor struct {
	// ID is the executor's workflow-unique identifier.
	ID string

	// ExecutorType identifies the original binding or implementation type for
	// diagnostics and telemetry.
	ExecutorType reflect.Type

	// CrossRunShareable indicates that this executor instance can be used by
	// multiple runs at the same time when it is bound as a shared instance.
	CrossRunShareable bool

	// Spec configures this executor's runtime behavior, routes, and lifecycle
	// hooks. Use [ExecutorSpec.Extend] to combine multiple spec fragments.
	Spec ExecutorSpec

	routerMu       sync.Mutex
	cachedProtocol *executorProtocol
	protocolErr    error
}

func (e *Executor) Initialize(ctx *Context) error {
	if e.Spec.Initialize != nil {
		return e.Spec.Initialize(ctx)
	}
	return nil
}

func (e *Executor) OnCheckpoint(ctx *Context) error {
	if e.Spec.OnCheckpoint != nil {
		return e.Spec.OnCheckpoint(ctx)
	}
	return nil
}

func (e *Executor) OnCheckpointRestored(ctx *Context) error {
	if e.Spec.OnCheckpointRestored != nil {
		return e.Spec.OnCheckpointRestored(ctx)
	}
	return nil
}

// OnMessageDeliveryStarting invokes all configured OnMessageDeliveryStarting
// hooks. Returns the first error from any hook.
func (e *Executor) OnMessageDeliveryStarting(ctx *Context) error {
	if e.Spec.OnMessageDeliveryStarting != nil {
		return e.Spec.OnMessageDeliveryStarting(ctx)
	}
	return nil
}

// OnMessageDeliveryFinished invokes all configured OnMessageDeliveryFinished
// hooks. All hooks are run; the first non-nil error encountered is returned
// after all have been invoked.
func (e *Executor) OnMessageDeliveryFinished(ctx *Context) error {
	if e.Spec.OnMessageDeliveryFinished != nil {
		return e.Spec.OnMessageDeliveryFinished(ctx)
	}
	return nil
}

func (e *Executor) Reset() error {
	if e.Spec.Reset != nil {
		return e.Spec.Reset()
	}
	return nil
}

func (e *Executor) protocol() (*executorProtocol, error) {
	e.routerMu.Lock()
	defer e.routerMu.Unlock()

	if e.cachedProtocol != nil || e.protocolErr != nil {
		return e.cachedProtocol, e.protocolErr
	}
	if !e.Spec.isConfigured() {
		panic(errors.New("executor has no protocol configuration function"))
	}
	bld := &ProtocolBuilder{}
	if e.Spec.ConfigureProtocol != nil {
		var err error
		bld, err = e.Spec.ConfigureProtocol(bld)
		if err != nil {
			e.protocolErr = err
			return nil, err
		}
		if bld == nil {
			e.protocolErr = errors.New("workflow: protocol configure function returned nil ProtocolBuilder")
			return nil, e.protocolErr
		}
	}
	e.cachedProtocol, e.protocolErr = bld.build(e.Spec)
	return e.cachedProtocol, e.protocolErr
}

func (e *Executor) router() (*messageRouter, error) {
	protocol, err := e.protocol()
	if err != nil {
		return nil, err
	}
	return protocol.router, nil
}

func (e *Executor) Execute(ctx *Context, message any) (result any, err error) {
	telemetry := ctx.telemetry()
	messageType := NewTypeID(reflect.TypeOf(message))
	spanCtx, span := telemetry.StartExecutorProcess(
		ctx,
		e.ID,
		observability.TypeName(e.ExecutorType),
		messageType.TypeName,
		message,
		ctx.traceContextStrings(),
	)
	defer func() {
		if err != nil {
			span.CaptureError(err)
		}
		span.End()
	}()
	if span != nil {
		bound := *ctx
		bound.Context = spanCtx
		ctx = &bound
	}

	if err := ctx.AddEvent(ExecutorInvokedEvent{ExecutorID: e.ID, Message: message}); err != nil {
		return nil, err
	}
	router, err := e.router()
	if err != nil {
		return nil, fmt.Errorf("error getting router for executor %q: %w", e.ID, err)
	}
	ret, found := router.routeMessage(ctx, message)
	if !found {
		return nil, fmt.Errorf("no handler found for message type %T", message)
	}
	if ret.Error != nil {
		if err := ctx.AddEvent(ExecutorFailedEvent{ExecutorID: e.ID, Error: ret.Error}); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("error invoking handler for message type %T: %w", message, ret.Error)
	} else {
		if err := ctx.AddEvent(ExecutorCompletedEvent{ExecutorID: e.ID, Result: ret.Result}); err != nil {
			return nil, err
		}
	}
	if ret.isVoid() {
		return nil, nil
	}
	// If we had a real return type, raise it as a SendMessage
	if ret.Result != nil {
		telemetry.SetExecutorOutput(span, ret.Result)
		if !e.Spec.DisableAutoSendMessageHandlerResultObject {
			if err := ctx.SendMessage("", ret.Result); err != nil {
				return nil, err
			}
		}
		if !e.Spec.DisableAutoYieldOutputHandlerResultObject {
			if err := ctx.YieldOutput(ret.Result); err != nil {
				return nil, err
			}
		}
	}
	return ret.Result, nil
}

func (e *Executor) describeProtocol() (ProtocolDescriptor, error) {
	protocol, err := e.protocol()
	if err != nil {
		return ProtocolDescriptor{}, err
	}
	return protocol.describe(), nil
}

func (e *Executor) DescribeProtocol() ProtocolDescriptor {
	protocol, err := e.describeProtocol()
	if err != nil {
		return ProtocolDescriptor{}
	}
	return protocol
}

// StatefulExecutorCache helps an executor read, update, and cache typed state.
//
// The cache reads from [Context.ReadOrInitState] when available, falling back
// to [Context.ReadState]. It caches state within an executor instance unless
// concurrent runs are enabled or the caller asks to skip the cache.
type StatefulExecutorCache[T any] struct {
	// StateKey is the workflow state key used for reads and queued updates.
	StateKey string
	// InitialStateFactory creates the state value used when no state has been
	// stored yet, or when a stored pointer/slice/map-like value is nil.
	InitialStateFactory func() T

	// ScopeName is the optional workflow state scope name.
	ScopeName string

	stateCache T
	cached     bool
}

func (s *StatefulExecutorCache[T]) cache(v T) {
	s.cached = true
	s.stateCache = v
}

func (s *StatefulExecutorCache[T]) initialState() T {
	if s.InitialStateFactory == nil {
		var zero T
		return zero
	}
	return s.InitialStateFactory()
}

func (s *StatefulExecutorCache[T]) normalizeState(state T) T {
	if isNilState(state) {
		return s.initialState()
	}
	return state
}

func (s *StatefulExecutorCache[T]) stateFromAny(v any) (T, error) {
	if v == nil {
		return s.initialState(), nil
	}
	state, ok := v.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("workflow: state %q has type %T, want %v", s.StateKey, v, reflect.TypeFor[T]())
	}
	return s.normalizeState(state), nil
}

func isNilState(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func (s *StatefulExecutorCache[T]) readOrInitState(ctx *Context) (T, error) {
	if ctx.ReadOrInitState != nil {
		state, err := ctx.ReadOrInitState(s.StateKey, s.ScopeName, func(context.Context, string, string) (any, error) {
			return s.initialState(), nil
		})
		if err != nil {
			var zero T
			return zero, err
		}
		return s.stateFromAny(state)
	}
	if ctx.ReadState != nil {
		state, err := ctx.ReadState(s.StateKey, s.ScopeName)
		if err != nil {
			var zero T
			return zero, err
		}
		return s.stateFromAny(state)
	}
	return s.initialState(), nil
}

// ReadState returns the current typed state value.
//
// When skipCache is false and concurrent runs are disabled, ReadState uses an
// executor-local cached value after the first read. When skipCache is true, or
// concurrent runs are enabled, it reads from workflow state each time.
func (s *StatefulExecutorCache[T]) ReadState(ctx *Context, skipCache bool) (T, error) {
	if !skipCache && !ctx.ConcurrentRunsEnabled {
		if s.cached {
			return s.stateCache, nil
		}
		state, err := s.readOrInitState(ctx)
		if err != nil {
			var zero T
			return zero, err
		}
		s.cache(state)
		return state, nil
	}
	return s.readOrInitState(ctx)
}

// QueueStateUpdate normalizes and queues a typed state update on the context.
//
// When concurrent runs are disabled, the in-memory cache is updated
// immediately so later reads in the same executor instance observe the new
// value.
func (s *StatefulExecutorCache[T]) QueueStateUpdate(ctx *Context, state T) error {
	state = s.normalizeState(state)
	if ctx.QueueStateUpdate != nil {
		if err := ctx.QueueStateUpdate(s.StateKey, s.ScopeName, state); err != nil {
			return err
		}
	}
	if !ctx.ConcurrentRunsEnabled {
		s.cache(state)
	}
	return nil
}

// InvokeWithState reads the current state, calls fn, and queues the returned
// state as an update.
func (s *StatefulExecutorCache[T]) InvokeWithState(ctx *Context, skipCache bool, fn func(ctx *Context, state T) (T, error)) error {
	state, err := s.ReadState(ctx, skipCache)
	if err != nil {
		return err
	}
	newState, err := fn(ctx, state)
	if err != nil {
		return err
	}
	return s.QueueStateUpdate(ctx, newState)
}

// Reset clears the executor-local cached state.
func (s *StatefulExecutorCache[T]) Reset() error {
	s.cached = false
	var zero T
	s.stateCache = zero
	return nil
}
