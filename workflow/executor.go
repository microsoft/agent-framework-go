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

	// SendTypes declares message types this executor may send with
	// [Context.SendMessage], in addition to handler result types automatically
	// sent when DisableAutoSendMessageHandlerResultObject is false.
	SendTypes []reflect.Type

	// YieldTypes declares output types this executor may yield with
	// [Context.YieldOutput], in addition to handler result types automatically
	// yielded when DisableAutoYieldOutputHandlerResultObject is false.
	YieldTypes []reflect.Type

	// ConfigureRoutes adds message handlers to the supplied builder.
	// It may return the same builder or a replacement builder.
	ConfigureRoutes func(builder *RouteBuilder) (*RouteBuilder, error)

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
// Existing route configuration and lifecycle hooks run before hooks from spec.
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
	s.SendTypes = appendUniqueTypes(s.SendTypes, spec.SendTypes...)
	s.YieldTypes = appendUniqueTypes(s.YieldTypes, spec.YieldTypes...)

	s.ConfigureRoutes = extendRoutes(s.ConfigureRoutes, spec.ConfigureRoutes)
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

func extendRoutes(first, second func(*RouteBuilder) (*RouteBuilder, error)) func(*RouteBuilder) (*RouteBuilder, error) {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(builder *RouteBuilder) (*RouteBuilder, error) {
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
	return s.ConfigureRoutes != nil ||
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

	routerMu     sync.Mutex
	cachedRouter *messageRouter
	routerErr    error

	declaredSendTypeCache sync.Map // reflect.Type -> reflect.Type
	canOutputCache        sync.Map // reflect.Type -> bool
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

func (e *Executor) router() (*messageRouter, error) {
	e.routerMu.Lock()
	defer e.routerMu.Unlock()

	if e.cachedRouter != nil || e.routerErr != nil {
		return e.cachedRouter, e.routerErr
	}
	if !e.Spec.isConfigured() {
		panic(errors.New("executor has no route configuration function"))
	}
	bld := &RouteBuilder{}
	if e.Spec.ConfigureRoutes != nil {
		var err error
		bld, err = e.Spec.ConfigureRoutes(bld)
		if err != nil {
			e.routerErr = err
			return nil, err
		}
	}
	e.cachedRouter, e.routerErr = bld.build()
	return e.cachedRouter, e.routerErr
}

func (e *Executor) Execute(ctx *Context, message any) (result any, err error) {
	telemetry := ctx.telemetry()
	messageType := NewTypeID(reflect.TypeOf(message))
	spanCtx, span := telemetry.StartExecutorProcess(
		ctx.GetContext(),
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
	ret, found := router.RouteMessage(ctx, message)
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
	if ret.IsVoid() {
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

func (e *Executor) InputTypes() []reflect.Type {
	router, err := e.router()
	if err != nil {
		return nil
	}
	return router.IncomingTypes()
}

func (e *Executor) OutputTypes() []reflect.Type {
	router, err := e.router()
	if err != nil {
		return nil
	}
	return e.yieldTypes(router)
}

// knownSentTypes are workflow system messages that every executor may send.
// They mirror .NET's MessageTypeTranslator.KnownSentTypes so executors can
// forward request-port control messages without declaring them as user protocol.
func knownSentTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[*ExternalRequest](),
		reflect.TypeFor[*ExternalResponse](),
	}
}

func (e *Executor) sendTypes(router *messageRouter) []reflect.Type {
	sends := appendUniqueTypes(nil, e.Spec.SendTypes...)
	if !e.Spec.DisableAutoSendMessageHandlerResultObject {
		sends = appendUniqueTypes(sends, slices.Collect(router.DefaultOutputTypes())...)
	}
	return sends
}

func (e *Executor) yieldTypes(router *messageRouter) []reflect.Type {
	yields := appendUniqueTypes(nil, e.Spec.YieldTypes...)
	if !e.Spec.DisableAutoYieldOutputHandlerResultObject {
		yields = appendUniqueTypes(yields, slices.Collect(router.DefaultOutputTypes())...)
	}
	return yields
}

// DeclaredSendType returns the protocol type that should be used when sending
// a message of typ from this executor. It reports false when typ is not allowed
// by the executor's declared send protocol. Interface send declarations match
// only that exact interface type, except any, which accepts every non-nil type.
func (e *Executor) DeclaredSendType(typ reflect.Type) (reflect.Type, bool) {
	if typ == nil {
		return nil, false
	}
	if cached, ok := e.declaredSendTypeCache.Load(typ); ok {
		return cached.(reflect.Type), true
	}
	router, err := e.router()
	if err != nil {
		return nil, false
	}
	for _, candidate := range appendUniqueTypes(e.sendTypes(router), knownSentTypes()...) {
		if declaredSendTypeMatches(typ, candidate) {
			e.declaredSendTypeCache.Store(typ, candidate)
			return candidate, true
		}
	}
	return nil, false
}

func declaredSendTypeMatches(typ reflect.Type, candidate reflect.Type) bool {
	if typ == nil || candidate == nil {
		return false
	}
	if typ == candidate || candidate == reflect.TypeFor[any]() {
		return true
	}
	return candidate.Kind() != reflect.Interface && typ.AssignableTo(candidate)
}

// CanSendType reports whether this executor can send messages of typ according
// to its declared send protocol.
func (e *Executor) CanSendType(typ reflect.Type) bool {
	_, ok := e.DeclaredSendType(typ)
	return ok
}

func (e *Executor) describeProtocol() (*ProtocolDescriptor, error) {
	router, err := e.router()
	if err != nil {
		return nil, err
	}
	return &ProtocolDescriptor{
		Accepts:    router.IncomingTypes(),
		Yields:     e.yieldTypes(router),
		Sends:      e.sendTypes(router),
		AcceptsAll: router.HasCatchAll(),
	}, nil
}

func (e *Executor) DescribeProtocol() *ProtocolDescriptor {
	protocol, err := e.describeProtocol()
	if err != nil {
		return &ProtocolDescriptor{}
	}
	return protocol
}

func (e *Executor) CanHandleTypeID(typ TypeID) bool {
	router, err := e.router()
	if err != nil {
		return false
	}
	return router.CanHandle(typ)
}

func (e *Executor) CanHandleType(typ reflect.Type) bool {
	return e.CanHandleTypeID(NewTypeID(typ))
}

func (e *Executor) CanOutputType(typ reflect.Type) bool {
	if cached, ok := e.canOutputCache.Load(typ); ok {
		return cached.(bool)
	}
	result := slices.ContainsFunc(e.OutputTypes(), typ.AssignableTo)
	e.canOutputCache.Store(typ, result)
	return result
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
