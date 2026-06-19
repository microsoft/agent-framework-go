// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// Executor is the runnable behavior for a workflow node. It owns the node's
// message protocol, lifecycle hooks, routing cache, and any executor-local
// state for one executor instance.
//
// An Executor is not the graph registration used by builders. Use
// [Executor.Bind] or [BindNewExecutorFunc] to produce an [ExecutorBinding].
// Bindings carry workflow identity and instance creation/reuse policy; runners
// call a binding to obtain an Executor for a workflow session.
//
// The zero value has no behavior. An [Executor] must have at least one
// non-nil route or lifecycle callback before it can build routes. Use
// [Executor.Extend] to add behavior from reusable helpers.
//
// Executors cache their route table after first use.
type Executor struct {
	// ID is the executor's workflow-unique identifier.
	ID string

	// ImplementationID identifies the implementation or semantic source for this executor.
	ImplementationID string

	// If true, the result of a message handler that returns a value will not be
	// sent as a message from the executor.
	DisableAutoSendMessageHandlerResultObject bool

	// If true, the result of a message handler that returns a value will not be
	// yielded as workflow output from the executor.
	DisableAutoYieldOutputHandlerResultObject bool

	// CrossRunShareable reports whether this executor instance can be shared
	// safely by concurrent workflow runs. [Executor.Bind] copies this value to
	// [ExecutorBinding.SupportsConcurrentSharedExecution].
	CrossRunShareable bool

	// ConfigureProtocol configures message handlers and declared send/yield
	// message types on the supplied builder.
	// It may return the same builder or a replacement builder.
	ConfigureProtocol func(builder *ProtocolBuilder) (*ProtocolBuilder, error)

	// Initialize is called when the executor instance is created for a run.
	InitializeFunc func(ctx *Context) error

	// AttachRuntimeFunc is called by an execution environment to attach
	// runtime-specific capabilities to this executor instance before Initialize.
	AttachRuntimeFunc func(runtime any) error

	// Reset clears any executor-local cached state before an instance is reused.
	ResetFunc func() error

	// Close releases executor-local resources when a workflow run ends.
	CloseFunc func(ctx context.Context) error

	// OnCheckpoint is called before workflow state is checkpointed.
	OnCheckpointFunc func(ctx *Context) error

	// OnCheckpointRestored is called after workflow state is restored.
	OnCheckpointRestoredFunc func(ctx *Context) error

	// OnMessageDeliveryStarting is invoked once per superstep, before any
	// messages are delivered to the executor. It is given a context bound to
	// this executor with no per-message trace context.
	OnMessageDeliveryStartingFunc func(ctx *Context) error

	// OnMessageDeliveryFinished is invoked once per superstep, after all
	// messages have been delivered to the executor (regardless of whether
	// individual deliveries succeeded). It is given a context bound to this
	// executor with no per-message trace context.
	OnMessageDeliveryFinishedFunc func(ctx *Context) error

	state executorState
}

// AttrYieldsOutput marks a struct-based executor as declaring an additional
// workflow output type T. Use it as a field on a struct passed to [NewExecutor].
// The field name is not significant; use _ when the field is only a marker.
type AttrYieldsOutput[T any] struct{ _ [0]*T }

// AttrSendsMessage marks a struct-based executor as declaring an additional
// sent message type T. Use it as a field on a struct passed to [NewExecutor].
// The field name is not significant; use _ when the field is only a marker.
type AttrSendsMessage[T any] struct{ _ [0]*T }

type executorState struct {
	protocolMu     sync.Mutex
	cachedProtocol *executorProtocol
	protocolErr    error
}

// SetCrossRunShareable sets whether e can be shared safely by concurrent
// workflow runs and returns e.
func (e *Executor) SetCrossRunShareable(v bool) *Executor {
	if e == nil {
		panic("workflow: cannot configure nil Executor")
	}
	e.CrossRunShareable = v
	return e
}

// Extend adds behavior from executor to e and returns e.
//
// Existing protocol configuration and lifecycle hooks run before hooks from executor.
// Most hooks stop on the first error. OnMessageDeliveryFinished runs every hook
// and returns the first error encountered.
//
// Runtime policy fields are combined conservatively: disabling automatic send
// or yield in either executor disables it in the receiver.
func (e *Executor) Extend(executor *Executor) *Executor {
	if e == nil {
		panic("workflow: cannot extend nil Executor")
	}
	if executor == nil {
		panic("workflow: cannot extend with nil Executor")
	}

	e.DisableAutoSendMessageHandlerResultObject = e.DisableAutoSendMessageHandlerResultObject || executor.DisableAutoSendMessageHandlerResultObject
	e.DisableAutoYieldOutputHandlerResultObject = e.DisableAutoYieldOutputHandlerResultObject || executor.DisableAutoYieldOutputHandlerResultObject
	e.ConfigureProtocol = extendProtocol(e.ConfigureProtocol, executor.ConfigureProtocol)
	e.InitializeFunc = extendContextHook(e.InitializeFunc, executor.InitializeFunc)
	e.AttachRuntimeFunc = extendRuntimeHook(e.AttachRuntimeFunc, executor.AttachRuntimeFunc)
	e.ResetFunc = extendResetHook(e.ResetFunc, executor.ResetFunc)
	e.CloseFunc = extendCloseHook(e.CloseFunc, executor.CloseFunc)
	e.OnCheckpointFunc = extendContextHook(e.OnCheckpointFunc, executor.OnCheckpointFunc)
	e.OnCheckpointRestoredFunc = extendContextHook(e.OnCheckpointRestoredFunc, executor.OnCheckpointRestoredFunc)
	e.OnMessageDeliveryStartingFunc = extendContextHook(e.OnMessageDeliveryStartingFunc, executor.OnMessageDeliveryStartingFunc)
	e.OnMessageDeliveryFinishedFunc = extendFinishedHook(e.OnMessageDeliveryFinishedFunc, executor.OnMessageDeliveryFinishedFunc)
	e.state = executorState{}
	return e
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

func extendRuntimeHook(first, second func(any) error) func(any) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(runtime any) error {
		if err := first(runtime); err != nil {
			return err
		}
		return second(runtime)
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

func extendCloseHook(first, second func(context.Context) error) func(context.Context) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(ctx context.Context) error {
		if err := first(ctx); err != nil {
			return err
		}
		return second(ctx)
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

func (e *Executor) isConfigured() bool {
	return e.ConfigureProtocol != nil ||
		e.InitializeFunc != nil ||
		e.AttachRuntimeFunc != nil ||
		e.ResetFunc != nil ||
		e.CloseFunc != nil ||
		e.OnCheckpointFunc != nil ||
		e.OnCheckpointRestoredFunc != nil ||
		e.OnMessageDeliveryStartingFunc != nil ||
		e.OnMessageDeliveryFinishedFunc != nil
}

// implementationID returns the binding implementation ID associated with e.
// The value is assigned by executor bindings when an executor instance is
// created, keeping workflow identity metadata read-only to callers.
func (e *Executor) implementationID() string {
	if e == nil {
		return ""
	}
	return e.ImplementationID
}

func (e *Executor) Initialize(ctx *Context) error {
	if e.InitializeFunc != nil {
		return e.InitializeFunc(ctx)
	}
	return nil
}

func (e *Executor) AttachRuntime(runtime any) error {
	if e.AttachRuntimeFunc != nil {
		return e.AttachRuntimeFunc(runtime)
	}
	return nil
}

func (e *Executor) OnCheckpoint(ctx *Context) error {
	if e.OnCheckpointFunc != nil {
		return e.OnCheckpointFunc(ctx)
	}
	return nil
}

func (e *Executor) OnCheckpointRestored(ctx *Context) error {
	if e.OnCheckpointRestoredFunc != nil {
		return e.OnCheckpointRestoredFunc(ctx)
	}
	return nil
}

// OnMessageDeliveryStarting invokes all configured OnMessageDeliveryStarting
// hooks. Returns the first error from any hook.
func (e *Executor) OnMessageDeliveryStarting(ctx *Context) error {
	if e.OnMessageDeliveryStartingFunc != nil {
		return e.OnMessageDeliveryStartingFunc(ctx)
	}
	return nil
}

// OnMessageDeliveryFinished invokes all configured OnMessageDeliveryFinished
// hooks. All hooks are run; the first non-nil error encountered is returned
// after all have been invoked.
func (e *Executor) OnMessageDeliveryFinished(ctx *Context) error {
	if e.OnMessageDeliveryFinishedFunc != nil {
		return e.OnMessageDeliveryFinishedFunc(ctx)
	}
	return nil
}

func (e *Executor) Reset() error {
	if e.ResetFunc != nil {
		return e.ResetFunc()
	}
	return nil
}

func (e *Executor) Close(ctx context.Context) error {
	if e.CloseFunc != nil {
		return e.CloseFunc(ctx)
	}
	return nil
}

func (e *Executor) protocol() (*executorProtocol, error) {
	state := &e.state
	state.protocolMu.Lock()
	defer state.protocolMu.Unlock()

	if state.cachedProtocol != nil || state.protocolErr != nil {
		return state.cachedProtocol, state.protocolErr
	}
	if !e.isConfigured() {
		panic(errors.New("executor has no protocol configuration function"))
	}
	bld := &ProtocolBuilder{}
	if e.ConfigureProtocol != nil {
		var err error
		bld, err = e.ConfigureProtocol(bld)
		if err != nil {
			state.protocolErr = err
			return nil, err
		}
		if bld == nil {
			state.protocolErr = errors.New("workflow: protocol configure function returned nil ProtocolBuilder")
			return nil, state.protocolErr
		}
	}
	state.cachedProtocol, state.protocolErr = bld.build(e)
	return state.cachedProtocol, state.protocolErr
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
		e.implementationID(),
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
	if ret.err != nil {
		if err := ctx.AddEvent(ExecutorFailedEvent{ExecutorID: e.ID, Error: ret.err}); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("error invoking handler for message type %T: %w", message, ret.err)
	} else {
		if err := ctx.AddEvent(ExecutorCompletedEvent{ExecutorID: e.ID, Result: ret.result}); err != nil {
			return nil, err
		}
	}
	if ret.isVoid() {
		return nil, nil
	}
	// If we had a real return type, raise it as a SendMessage
	if ret.result != nil && ret.autoOutput {
		telemetry.SetExecutorOutput(span, ret.result)
		if !e.DisableAutoSendMessageHandlerResultObject {
			if err := ctx.SendMessage("", ret.result); err != nil {
				return nil, err
			}
		}
		if !e.DisableAutoYieldOutputHandlerResultObject {
			if err := ctx.YieldOutput(ret.result); err != nil {
				return nil, err
			}
		}
	}
	return ret.result, nil
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

// newRequestPortExecutor creates the executor that turns request-port messages
// into [ExternalRequest]s and routes matching [ExternalResponse]s back into the
// workflow.
func newRequestPortExecutor(port RequestPort) *Executor {
	var (
		wrappedMu       sync.Mutex
		wrappedRequests = make(map[string]*ExternalRequest)
	)

	return &Executor{
		ID: port.ID,
		ConfigureProtocol: func(rb *ProtocolBuilder) (*ProtocolBuilder, error) {
			rb.SendsMessageType(port.Response, reflect.TypeFor[*ExternalResponse]())
			rb.RouteBuilder.
				AddHandlerRaw(port.Request, nil, func(ctx *Context, msg any) (any, error) {
					req, err := NewExternalRequest("", port, msg)
					if err != nil {
						return nil, err
					}
					if err := ctx.PostRequest(req); err != nil {
						return nil, err
					}
					return nil, nil
				}).
				AddHandlerRaw(reflect.TypeFor[*ExternalRequest](), nil, func(ctx *Context, msg any) (any, error) {
					req := msg.(*ExternalRequest)
					data, ok := req.Data.As(port.Request)
					if !ok {
						return nil, fmt.Errorf("message type %v could not be interpreted as request type %v", req.PortInfo.RequestType, port.Request)
					}
					if !req.PortInfo.ResponseType.Match(port.Response) {
						return nil, fmt.Errorf("response type %v is not valid for request port response type %v", port.Response, req.PortInfo.ResponseType)
					}
					wrapped, err := NewExternalRequest(req.RequestID, port, data)
					if err != nil {
						return nil, err
					}
					wrappedMu.Lock()
					wrappedRequests[req.RequestID] = req
					wrappedMu.Unlock()
					if err := ctx.PostRequest(wrapped); err != nil {
						return nil, err
					}
					return nil, nil
				}).
				AddHandlerRaw(reflect.TypeFor[*ExternalResponse](), nil, func(ctx *Context, msg any) (any, error) {
					resp := msg.(*ExternalResponse)
					if resp.PortInfo.PortID != port.ID {
						return nil, nil
					}
					data, ok := resp.Data.As(port.Response)
					if !ok {
						return nil, fmt.Errorf("expected response of type %v, got %T", port.Response, resp.Data.Any())
					}
					wrappedMu.Lock()
					original, wrapped := wrappedRequests[resp.RequestID]
					delete(wrappedRequests, resp.RequestID)
					wrappedMu.Unlock()
					if wrapped {
						return nil, ctx.SendMessage("", &ExternalResponse{
							PortInfo:  original.PortInfo,
							RequestID: resp.RequestID,
							Data:      resp.Data,
						})
					}
					if err := ctx.SendMessage("", resp); err != nil {
						return nil, err
					}
					return nil, ctx.SendMessage("", data)
				})
			return rb, nil
		},
	}
}

func bindFuncImplementationID(id string, fn any) string {
	value := reflect.ValueOf(fn)
	if value.Kind() != reflect.Func || value.IsNil() {
		return id
	}
	runtimeFunc := runtime.FuncForPC(value.Pointer())
	if runtimeFunc == nil {
		return id
	}
	name := strings.TrimSuffix(runtimeFunc.Name(), "-fm")
	if name == "" || isAnonymousFuncName(name) {
		return id
	}
	return name
}

func isAnonymousFuncName(name string) bool {
	return containsAnonymousFuncSegment(name, ".func")
}

func containsAnonymousFuncSegment(name string, marker string) bool {
	for {
		idx := strings.Index(name, marker)
		if idx < 0 {
			return false
		}
		if hasAnonymousFuncOrdinal(name[idx+len(marker):]) {
			return true
		}
		name = name[idx+len(marker):]
	}
}

func hasAnonymousFuncOrdinal(s string) bool {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[i] == '.'
		}
	}
	return true
}

// NewExecutor converts v into an [Executor]. The id parameter is the workflow
// executor ID to assign or validate. If id is empty for an existing executor,
// the existing executor ID is used.
//
// NewExecutor accepts these values:
//   - *[Executor], which is returned as an executor instance after checking
//     that a non-empty id matches the executor's ID.
//   - [RequestPort] or *[RequestPort], which create the executor used by
//     [RequestPort.Bind].
//   - [ExecutorBinding], which is instantiated through the binding and must
//     produce an executor with the binding ID. A non-empty id must match the
//     binding ID.
//   - Struct values or pointers to structs with a Handle method. Handle must be
//     a valid function handler shape accepted by NewExecutor.
//   - Function handlers must have exactly one typed non-context input parameter
//     and may return zero or one typed output parameter.
//   - A *[Context] input is optional; when present, it must be the first input
//     parameter.
//   - A final error output is treated as the handler error; a non-final error
//     output is allowed only as the single ordinary output value.
//   - Variadic functions are not supported; define an explicit slice or struct
//     input type instead.
//   - Function executors register the non-context input type as the accepted
//     message type and the non-handler-error output type, when present, as an
//     auto-sent and auto-yielded output type.
//   - Named functions use their runtime function name as implementation
//     identity; anonymous functions use id. Struct-based executors use their
//     struct type name.
//   - Struct-based executors may declare additional protocol types using
//     [AttrSendsMessage] and [AttrYieldsOutput] fields. Field names are not
//     significant; _ is recommended for marker-only fields.
//
// NewExecutor panics for nil values, nil functions, unsupported function
// signatures, or mismatched IDs.
func NewExecutor(id string, v any) *Executor {
	if v == nil {
		panic("workflow: cannot create Executor from nil value")
	}
	switch v := v.(type) {
	case *Executor:
		if v == nil {
			panic("workflow: cannot create Executor from nil *Executor")
		}
		if id != "" && v.ID != id {
			panic(fmt.Sprintf("workflow: executor ID %q does not match provided ID %q", v.ID, id))
		}
		return v
	case RequestPort:
		return newRequestPortExecutor(v)
	case *RequestPort:
		if v == nil {
			panic("workflow: cannot create Executor from nil *RequestPort")
		}
		return newRequestPortExecutor(*v)
	case ExecutorBinding:
		if id != "" && v.ID != id {
			panic(fmt.Sprintf("workflow: binding ID %q does not match provided ID %q", v.ID, id))
		}
		executor, err := v.CreateInstance(v.ID)
		if err != nil {
			panic(fmt.Sprintf("workflow: error creating Executor from binding for ID %q: %v", v.ID, err))
		}
		return executor
	default:
		value := reflect.ValueOf(v)
		switch value.Kind() {
		case reflect.Func:
			if value.IsNil() {
				panic("workflow: cannot create Executor from nil function")
			}
			fn, err := newReflectExecutorFunc(value)
			if err != nil {
				panic(err)
			}
			return newReflectFunctionExecutor(id, v, fn)
		case reflect.Struct, reflect.Pointer:
			executor, ok, err := newStructHandleExecutor(id, value)
			if err != nil {
				panic(err)
			}
			if ok {
				return executor
			}
			panic(fmt.Sprintf("workflow: cannot create Executor from value of type %T", v))
		default:
			panic(fmt.Sprintf("workflow: cannot create Executor from value of type %T", v))
		}
	}
}

func newReflectFunctionExecutor(id string, source any, fn reflectExecutorFunc) *Executor {
	return &Executor{
		ID:               id,
		ImplementationID: bindFuncImplementationID(id, source),
		ConfigureProtocol: func(rb *ProtocolBuilder) (*ProtocolBuilder, error) {
			rb.RouteBuilder.AddHandlerRaw(fn.inputType, fn.outputType, func(ctx *Context, msg any) (any, error) {
				if msg == nil {
					if !isNilType(fn.inputType) {
						return nil, fmt.Errorf("workflow: cannot use nil message as %v", fn.inputType)
					}
					output, ok, err := fn.invoke(ctx, reflect.Zero(fn.inputType))
					if err != nil || !ok {
						return nil, err
					}
					return output.Interface(), nil
				}
				input := reflect.ValueOf(msg)
				if !input.Type().AssignableTo(fn.inputType) {
					return nil, fmt.Errorf("workflow: message type %T is not assignable to %v", msg, fn.inputType)
				}
				output, ok, err := fn.invoke(ctx, input)
				if err != nil || !ok {
					return nil, err
				}
				return output.Interface(), nil
			})
			return rb, nil
		},
	}
}

func newStructHandleExecutor(id string, value reflect.Value) (*Executor, bool, error) {
	structType, ok, err := structTypeForExecutorValue(value)
	if err != nil || !ok {
		return nil, ok, err
	}
	sendTypes, yieldTypes := executorAttrTypes(structType)
	methodValue := handleMethodValue(value)
	if !methodValue.IsValid() {
		if len(sendTypes) != 0 || len(yieldTypes) != 0 {
			return nil, true, fmt.Errorf("workflow: struct executor type %v has executor attributes but no Handle method", structType)
		}
		return nil, false, nil
	}
	fn, err := newReflectExecutorFunc(methodValue)
	if err != nil {
		return nil, true, err
	}
	executor := newReflectFunctionExecutor(id, methodValue.Interface(), fn)
	executor.ImplementationID = structExecutorImplementationID(structType)
	if len(sendTypes) == 0 && len(yieldTypes) == 0 {
		return executor, true, nil
	}
	executor.Extend(&Executor{ConfigureProtocol: func(rb *ProtocolBuilder) (*ProtocolBuilder, error) {
		rb.SendsMessageType(sendTypes...)
		rb.YieldsOutputType(yieldTypes...)
		return rb, nil
	}})
	return executor, true, nil
}

func structExecutorImplementationID(structType reflect.Type) string {
	if name := structType.Name(); name != "" {
		return name
	}
	return structType.String()
}

func structTypeForExecutorValue(value reflect.Value) (reflect.Type, bool, error) {
	typ := value.Type()
	if typ.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, true, fmt.Errorf("workflow: cannot create Executor from nil %v", typ)
		}
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil, false, nil
	}
	return typ, true, nil
}

func handleMethodValue(value reflect.Value) reflect.Value {
	method := value.MethodByName("Handle")
	if method.IsValid() || value.Kind() != reflect.Struct {
		return method
	}
	ptr := reflect.New(value.Type())
	ptr.Elem().Set(value)
	return ptr.MethodByName("Handle")
}

func executorAttrTypes(structType reflect.Type) (sendTypes []reflect.Type, yieldTypes []reflect.Type) {
	for i := 0; i < structType.NumField(); i++ {
		fieldType := structType.Field(i).Type
		if typ, ok := executorAttrType(fieldType, "AttrSendsMessage"); ok {
			sendTypes = appendUniqueTypes(sendTypes, typ)
		}
		if typ, ok := executorAttrType(fieldType, "AttrYieldsOutput"); ok {
			yieldTypes = appendUniqueTypes(yieldTypes, typ)
		}
	}
	return sendTypes, yieldTypes
}

func executorAttrType(typ reflect.Type, name string) (reflect.Type, bool) {
	if typ.PkgPath() != reflect.TypeFor[AttrYieldsOutput[struct{}]]().PkgPath() || !strings.HasPrefix(typ.Name(), name+"[") || typ.Kind() != reflect.Struct || typ.NumField() != 1 {
		return nil, false
	}
	fieldType := typ.Field(0).Type
	if fieldType.Kind() != reflect.Array || fieldType.Len() != 0 || fieldType.Elem().Kind() != reflect.Pointer {
		return nil, false
	}
	return fieldType.Elem().Elem(), true
}

type reflectExecutorFunc struct {
	fnValue     reflect.Value
	withContext bool
	inputType   reflect.Type
	outputType  reflect.Type
	hasError    bool
}

func newReflectExecutorFunc(fnValue reflect.Value) (reflectExecutorFunc, error) {
	fnType := fnValue.Type()
	contextType := reflect.TypeFor[*Context]()
	errorType := reflect.TypeFor[error]()

	withContext := fnType.NumIn() > 0 && fnType.In(0) == contextType
	for i := 1; i < fnType.NumIn(); i++ {
		if fnType.In(i) == contextType {
			return reflectExecutorFunc{}, fmt.Errorf("workflow: *Context must be the first input parameter for function with type %v", fnType)
		}
	}
	inputStart := 0
	if withContext {
		inputStart = 1
	}
	if fnType.IsVariadic() {
		return reflectExecutorFunc{}, fmt.Errorf("workflow: variadic functions are not supported for function with type %v", fnType)
	}
	if fnType.NumIn()-inputStart != 1 {
		return reflectExecutorFunc{}, fmt.Errorf("workflow: function with type %v must have exactly one non-context input parameter", fnType)
	}
	inputType := fnType.In(inputStart)

	outputEnd := fnType.NumOut()
	hasError := outputEnd > 0 && fnType.Out(outputEnd-1).AssignableTo(errorType)
	if hasError {
		outputEnd--
	}
	if outputEnd > 1 {
		return reflectExecutorFunc{}, fmt.Errorf("workflow: function with type %v must return at most one non-error output parameter", fnType)
	}
	var outputType reflect.Type
	if outputEnd == 1 {
		outputType = fnType.Out(0)
	}

	return reflectExecutorFunc{
		fnValue:     fnValue,
		withContext: withContext,
		inputType:   inputType,
		outputType:  outputType,
		hasError:    hasError,
	}, nil
}

func (fn reflectExecutorFunc) invoke(ctx *Context, input reflect.Value) (reflect.Value, bool, error) {
	var args [2]reflect.Value
	argCount := 0
	if fn.withContext {
		args[argCount] = reflect.ValueOf(ctx)
		argCount++
	}
	args[argCount] = input
	argCount++

	results := fn.fnValue.Call(args[:argCount])
	if fn.hasError {
		resultErr := results[len(results)-1]
		if !isNilType(resultErr.Type()) || !resultErr.IsNil() {
			return reflect.Value{}, false, resultErr.Interface().(error)
		}
		results = results[:len(results)-1]
	}
	if len(results) == 0 {
		return reflect.Value{}, false, nil
	}
	return results[0], true, nil
}

func isNilType(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}
