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

// ExecutorOptions holds configuration options for [Executor] behavior.
type ExecutorOptions struct {
	// If true, the result of a message handler that returns a value will be sent as a message from the executor.
	DisableAutoSendMessageHandlerResultObject bool

	// If true, the result of a message handler that returns a value will be yielded as an output of the executor.
	DisableAutoYieldOutputHandlerResultObject bool

	// If true, the executor may be used simultaneously by multiple runs safely.
	CrossRunShareable bool
}

type callResult struct {
	Result any
	Error  error
}

func (cr callResult) Handled() bool {
	return cr != callResult{}
}

func (cr callResult) IsVoid() bool {
	return cr.Result != nil && reflect.TypeOf(cr.Result) == reflect.TypeFor[struct{}]()
}

func (cr callResult) Canceled() bool {
	return errors.Is(cr.Error, context.Canceled)
}

type ExecutorConfig struct {
	ConfigureRoutes      func(builder *RouteBuilder) (*RouteBuilder, error)
	Initialize           func(ctx *Context) error
	Reset                func() error
	OnCheckpoint         func(ctx *Context) error
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

type Executor struct {
	ID           string
	ExecutorType reflect.Type
	Options      ExecutorOptions

	Config []*ExecutorConfig

	cachedRouter *messageRouter
	routerErr    error

	canOutputCache sync.Map // reflect.Type -> bool
}

func (e *Executor) Initialize(ctx *Context) error {
	for _, cfg := range e.Config {
		if cfg.Initialize != nil {
			if err := cfg.Initialize(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Executor) OnCheckpoint(ctx *Context) error {
	for _, cfg := range e.Config {
		if cfg.OnCheckpoint != nil {
			if err := cfg.OnCheckpoint(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Executor) OnCheckpointRestored(ctx *Context) error {
	for _, cfg := range e.Config {
		if cfg.OnCheckpointRestored != nil {
			if err := cfg.OnCheckpointRestored(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// OnMessageDeliveryStarting invokes all configured OnMessageDeliveryStarting
// hooks. Returns the first error from any hook.
func (e *Executor) OnMessageDeliveryStarting(ctx *Context) error {
	for _, cfg := range e.Config {
		if cfg.OnMessageDeliveryStarting != nil {
			if err := cfg.OnMessageDeliveryStarting(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// OnMessageDeliveryFinished invokes all configured OnMessageDeliveryFinished
// hooks. All hooks are run; the first non-nil error encountered is returned
// after all have been invoked.
func (e *Executor) OnMessageDeliveryFinished(ctx *Context) error {
	var firstErr error
	for _, cfg := range e.Config {
		if cfg.OnMessageDeliveryFinished != nil {
			if err := cfg.OnMessageDeliveryFinished(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (e *Executor) Reset() error {
	for _, cfg := range e.Config {
		if cfg.Reset != nil {
			if err := cfg.Reset(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Executor) router() (*messageRouter, error) {
	if e.cachedRouter != nil || e.routerErr != nil {
		return e.cachedRouter, e.routerErr
	}
	if len(e.Config) == 0 {
		panic(errors.New("executor has no route configuration function"))
	}
	bld := &RouteBuilder{}
	for _, cfg := range e.Config {
		if cfg.ConfigureRoutes == nil {
			continue
		}
		var err error
		bld, err = cfg.ConfigureRoutes(bld)
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
		if !e.Options.DisableAutoSendMessageHandlerResultObject {
			if err := ctx.SendMessage("", ret.Result); err != nil {
				return nil, err
			}
		}
		if !e.Options.DisableAutoYieldOutputHandlerResultObject {
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
	return slices.Collect(router.DefaultOutputTypes())
}

func (e *Executor) DescribeProtocol() *ProtocolDescriptor {
	types := e.InputTypes()
	return &ProtocolDescriptor{Accepts: types}
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

func (e *Executor) CanHandle(v any) bool {
	return e.CanHandleType(reflect.TypeOf(v))
}

func (e *Executor) CanOutputType(typ reflect.Type) bool {
	if cached, ok := e.canOutputCache.Load(typ); ok {
		return cached.(bool)
	}
	result := slices.ContainsFunc(e.OutputTypes(), typ.AssignableTo)
	e.canOutputCache.Store(typ, result)
	return result
}

type StatefulExecutorCache[T any] struct {
	// Required fields
	StateKey            string
	InitialStateFactory func() T

	// Optional fields
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

func (s *StatefulExecutorCache[T]) Reset() error {
	s.cached = false
	var zero T
	s.stateCache = zero
	return nil
}
