// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"reflect"
	"slices"
)

// CatchAllFunc handles messages that do not match a typed route.
//
// The message is supplied as a [PortableValue] so catch-all handlers can
// forward or inspect messages even when their concrete Go type is not known to
// the current executor.
type CatchAllFunc func(*Context, PortableValue) (any, error)

// MessageHandlerFunc handles a routed workflow message.
//
// The handler receives the concrete message value registered with
// [RouteBuilder.AddHandlerRaw]. Its return value may be forwarded as a message
// or yielded as output depending on the executor's [ExecutorSpec].
type MessageHandlerFunc func(*Context, any) (any, error)

// AddHandlerOption configures handler registration for [RouteBuilder.AddHandlerRaw]
// and [RouteBuilder.AddCatchAll].
type AddHandlerOption func(*addHandlerOptions)

// addHandlerOptions stores the normalized options for a handler registration.
type addHandlerOptions struct {
	overwrite bool
}

// WithHandlerOverwrite controls whether handler registration replaces an
// existing handler.
//
// For [RouteBuilder.AddHandlerRaw], overwrite applies to the handler registered
// for the same message type. For [RouteBuilder.AddCatchAll], overwrite applies
// to the single catch-all handler.
func WithHandlerOverwrite(overwrite bool) AddHandlerOption {
	return func(options *addHandlerOptions) {
		options.overwrite = overwrite
	}
}

// RouteBuilder configures the message routes for an executor.
//
// A RouteBuilder is normally used from [ExecutorSpec.ConfigureRoutes] to
// register typed handlers with [RouteBuilder.AddHandlerRaw] and an optional
// fallback handler with [RouteBuilder.AddCatchAll]. The zero value is ready to
// use, and registration methods return the builder so calls can be chained.
// Invalid registrations are recorded on the builder and returned when the
// executor builds its router.
type RouteBuilder struct {
	handlers    map[reflect.Type]MessageHandlerFunc
	outputTypes map[reflect.Type]reflect.Type
	catchAll    CatchAllFunc
	err         error

	built bool
}

// AddHandlerRaw registers a typed handler using explicit runtime types.
//
// messageType is the concrete message type accepted by handler. outputType, when
// non-nil, declares the handler result type used for workflow protocol discovery
// when automatic send or yield of handler return values is enabled. Handler
// results are still runtime values; outputType is metadata.
//
// By default, registering a duplicate message type is an error; use
// [WithHandlerOverwrite] to replace an existing handler.
func (rb *RouteBuilder) AddHandlerRaw(messageType reflect.Type, outputType reflect.Type, handler MessageHandlerFunc, options ...AddHandlerOption) *RouteBuilder {
	if messageType == nil {
		panic("messageType cannot be nil")
	}
	if handler == nil {
		panic("handler cannot be nil")
	}
	addHandlerOptions := addHandlerOptions{}
	for _, option := range options {
		if option != nil {
			option(&addHandlerOptions)
		}
	}
	if rb.err != nil {
		return rb
	}
	if reflect.TypeOf(messageType) == reflect.TypeFor[PortableValue]() {
		rb.err = errors.New("cannot register a handler for PortableValue. Use AddCatchAll() instead")
		return rb
	}
	// Overwrite must be false if the type is not registered. Overwrite must be true if the type is registered.
	if _, exists := rb.handlers[messageType]; exists == addHandlerOptions.overwrite {
		if rb.handlers == nil {
			rb.handlers = make(map[reflect.Type]MessageHandlerFunc)
		}
		rb.handlers[messageType] = handler
		if outputType != nil {
			if rb.outputTypes == nil {
				rb.outputTypes = make(map[reflect.Type]reflect.Type)
			}
			rb.outputTypes[messageType] = outputType
		} else {
			delete(rb.outputTypes, messageType)
		}
	} else if addHandlerOptions.overwrite {
		// overwrite is true, but the type is not registered.
		rb.err = fmt.Errorf("cannot overwrite handler for unregistered type %s", messageType)
		return rb
	} else if !addHandlerOptions.overwrite {
		// overwrite is false, but the type is already registered.
		rb.err = fmt.Errorf("handler for type %s is already registered", messageType)
		return rb
	}
	return rb
}

// AddCatchAll registers a fallback handler for messages without a typed handler.
//
// The catch-all handler receives the message as a [PortableValue]. If the
// message was not already portable, the router wraps it with [AnyPortableValue]
// before invoking the handler.
//
// By default, registering a second catch-all handler is an error; use
// [WithHandlerOverwrite] to replace an existing catch-all handler.
func (rb *RouteBuilder) AddCatchAll(handler func(*Context, PortableValue) (any, error), options ...AddHandlerOption) *RouteBuilder {
	if handler == nil {
		panic("handler cannot be nil")
	}
	addHandlerOptions := addHandlerOptions{}
	for _, option := range options {
		if option != nil {
			option(&addHandlerOptions)
		}
	}
	if rb.err != nil {
		return rb
	}
	if rb.catchAll != nil && !addHandlerOptions.overwrite {
		rb.err = errors.New("catch-all handler is already registered")
		return rb
	}
	rb.catchAll = handler
	return rb
}

// build materializes a RouteBuilder into a message router.
//
// Any registration error recorded on the builder is returned here. This lets
// route configuration code chain calls and report the first invalid
// registration when the executor builds its router.
func (rb *RouteBuilder) build() (*messageRouter, error) {
	rb.built = true
	if rb.err != nil {
		return nil, rb.err
	}
	defaultOutputTypes := make(map[reflect.Type]struct{}, len(rb.outputTypes))
	for _, outType := range rb.outputTypes {
		defaultOutputTypes[outType] = struct{}{}
	}
	router := newMessageRouter(rb.handlers, defaultOutputTypes, rb.catchAll)
	return router, nil
}

// messageRouter resolves runtime messages to their configured handlers.
//
// It keeps both reflect.Type keys for in-process values and TypeID keys for
// portable values restored from serialized workflow state.
type messageRouter struct {
	typedHandlers      map[reflect.Type]MessageHandlerFunc
	runtimeTypeMap     map[TypeID]reflect.Type
	catchAllFunc       CatchAllFunc
	defaultOutputTypes map[reflect.Type]struct{}
}

// newMessageRouter creates a router and indexes typed handlers by TypeID.
func newMessageRouter(handlers map[reflect.Type]MessageHandlerFunc, outputTypes map[reflect.Type]struct{}, catchAll CatchAllFunc) *messageRouter {
	runtimeTypeMap := make(map[TypeID]reflect.Type, len(handlers))
	for msgType := range handlers {
		typeID := NewTypeID(msgType)
		runtimeTypeMap[typeID] = msgType
	}
	return &messageRouter{
		typedHandlers:      handlers,
		runtimeTypeMap:     runtimeTypeMap,
		catchAllFunc:       catchAll,
		defaultOutputTypes: outputTypes,
	}
}

// IncomingTypes returns the concrete message types with typed handlers.
func (mr *messageRouter) IncomingTypes() []reflect.Type {
	return slices.Collect(maps.Keys(mr.typedHandlers))
}

// DefaultOutputTypes returns the output types declared by typed handlers.
func (mr *messageRouter) DefaultOutputTypes() iter.Seq[reflect.Type] {
	return maps.Keys(mr.defaultOutputTypes)
}

// HasCatchAll reports whether the router has a fallback handler.
func (mr *messageRouter) HasCatchAll() bool {
	return mr.catchAllFunc != nil
}

// CanHandle reports whether typ can be routed by a typed handler or catch-all.
func (mr *messageRouter) CanHandle(typ TypeID) bool {
	if mr.catchAllFunc != nil {
		return true
	}
	_, ok := mr.runtimeTypeMap[typ]
	return ok
}

// RouteMessage invokes the matching typed handler or catch-all handler.
//
// Portable values are unpacked to their registered runtime type when possible.
// Panics raised by handlers are converted to error call results so executor
// error handling can treat them like ordinary handler failures.
func (mr *messageRouter) RouteMessage(ctx *Context, msg any) (result callResult, handled bool) {
	if msg == nil {
		panic("nil message")
	}
	pvalue, isPortable := msg.(PortableValue)
	if isPortable {
		if typ, ok := mr.runtimeTypeMap[pvalue.TypeID]; ok {
			if v, ok := pvalue.As(typ); ok {
				// If we found a runtime type, we can use it
				msg = v
			}
		}
	}
	defer func() {
		if r := recover(); r != nil {
			err, ok := r.(error)
			if !ok {
				err = fmt.Errorf("panic: %v", r)
			}
			result = callResult{Error: err, Result: struct{}{}}
		}
	}()
	if handler, ok := mr.typedHandlers[reflect.TypeOf(msg)]; ok {
		handled = true
		ret, err := handler(ctx, msg)
		return callResult{ret, err}, handled
	}
	if mr.catchAllFunc != nil {
		handled = true
		if !isPortable {
			pvalue = AnyPortableValue(msg)
		}
		ret, err := mr.catchAllFunc(ctx, pvalue)
		return callResult{ret, err}, handled
	}
	return callResult{}, false
}

// callResult captures a handler invocation result and error.
type callResult struct {
	Result any
	Error  error
}

// IsVoid reports whether the handler returned the workflow void sentinel.
func (cr callResult) IsVoid() bool {
	return cr.Result != nil && reflect.TypeOf(cr.Result) == reflect.TypeFor[struct{}]()
}

// Canceled reports whether the handler failed because its context was canceled.
func (cr callResult) Canceled() bool {
	return errors.Is(cr.Error, context.Canceled)
}
