// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"errors"
	"fmt"
	"iter"
	"maps"
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/internal/concurrent"
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
// or yielded as output depending on the executor's [Executor].
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
// A RouteBuilder is normally used from [Executor.ConfigureProtocol] to
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
// and automatic send/yield of handler return values. A nil outputType registers
// an action-style handler: it may use [Context] to send messages or yield
// outputs manually, but its handler return value is not auto-forwarded.
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
	router := newMessageRouter(rb.handlers, rb.outputTypes, rb.catchAll)
	return router, nil
}

// messageRouter resolves runtime messages to their configured handlers.
//
// It keeps exact reflect.Type handlers, indexes handler metadata by TypeID for
// portable values restored from serialized workflow state, and resolves
// assignable interface handlers for in-process values.
type messageRouter struct {
	typedHandlers     map[reflect.Type]typeHandlingInfo
	typeInfos         concurrent.Map[TypeID, typeHandlingInfo]
	interfaceHandlers []reflect.Type
	catchAllFunc      CatchAllFunc
	outputTypes       map[reflect.Type]struct{}
}

type typeHandlingInfo struct {
	runtimeType reflect.Type
	handler     MessageHandlerFunc
	autoOutput  bool
}

func (info typeHandlingInfo) forRuntimeType(runtimeType reflect.Type) typeHandlingInfo {
	info.runtimeType = runtimeType
	return info
}

// newMessageRouter creates a router and indexes typed handlers by TypeID.
func newMessageRouter(handlers map[reflect.Type]MessageHandlerFunc, handlerOutputTypes map[reflect.Type]reflect.Type, catchAll CatchAllFunc) *messageRouter {
	outputTypes := make(map[reflect.Type]struct{}, len(handlerOutputTypes))
	for _, outType := range handlerOutputTypes {
		outputTypes[outType] = struct{}{}
	}
	router := &messageRouter{
		typedHandlers: make(map[reflect.Type]typeHandlingInfo, len(handlers)),
		catchAllFunc:  catchAll,
		outputTypes:   outputTypes,
	}
	for msgType, handler := range handlers {
		_, autoOutput := handlerOutputTypes[msgType]
		info := typeHandlingInfo{runtimeType: msgType, handler: handler, autoOutput: autoOutput}
		router.typedHandlers[msgType] = info
		router.typeInfos.Store(NewTypeID(msgType), info)
		if msgType.Kind() == reflect.Interface {
			router.interfaceHandlers = append(router.interfaceHandlers, msgType)
		}
	}
	return router
}

// incomingTypes returns the concrete message types with typed handlers.
func (mr *messageRouter) incomingTypes() []reflect.Type {
	return slices.Collect(maps.Keys(mr.typedHandlers))
}

// defaultOutputTypes returns the output types declared by typed handlers.
func (mr *messageRouter) defaultOutputTypes() iter.Seq[reflect.Type] {
	return maps.Keys(mr.outputTypes)
}

// hasCatchAll reports whether the router has a fallback handler.
func (mr *messageRouter) hasCatchAll() bool {
	return mr.catchAllFunc != nil
}

// canHandle reports whether typ can be routed by a typed handler or catch-all.
func (mr *messageRouter) canHandle(typ TypeID) bool {
	if mr.catchAllFunc != nil {
		return true
	}
	_, ok := mr.typeInfo(typ)
	return ok
}

// canHandleType reports whether typ can be routed by an exact typed handler,
// an assignable interface handler, or catch-all.
func (mr *messageRouter) canHandleType(typ reflect.Type) bool {
	if typ == nil {
		return false
	}
	if mr.catchAllFunc != nil {
		return true
	}
	_, ok := mr.findHandler(typ)
	return ok
}

// routeMessage invokes the matching typed handler or catch-all handler.
//
// Portable values are unpacked to their registered runtime type when possible.
// Panics raised by handlers are converted to error call results so executor
// error handling can treat them like ordinary handler failures.
func (mr *messageRouter) routeMessage(ctx *Context, msg any) (result callResult, handled bool) {
	if msg == nil {
		panic("nil message")
	}
	pvalue, isPortable := msg.(PortableValue)
	if isPortable {
		if info, ok := mr.typeInfo(pvalue.TypeID); ok {
			if v, ok := pvalue.As(info.runtimeType); ok {
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
			result = callResult{err: err, result: struct{}{}}
		}
	}()
	if info, ok := mr.findHandler(reflect.TypeOf(msg)); ok {
		handled = true
		ret, err := info.handler(ctx, msg)
		return callResult{result: ret, err: err, autoOutput: info.autoOutput}, handled
	}
	if mr.catchAllFunc != nil {
		handled = true
		if !isPortable {
			pvalue = AnyPortableValue(msg)
		}
		ret, err := mr.catchAllFunc(ctx, pvalue)
		return callResult{result: ret, err: err, autoOutput: true}, handled
	}
	return callResult{}, false
}

func (mr *messageRouter) typeInfo(typeID TypeID) (typeHandlingInfo, bool) {
	if typeID == (TypeID{}) {
		return typeHandlingInfo{}, false
	}
	return mr.typeInfos.Load(typeID)
}

func (mr *messageRouter) findHandler(messageType reflect.Type) (typeHandlingInfo, bool) {
	if messageType == nil {
		return typeHandlingInfo{}, false
	}
	if info, ok := mr.typedHandlers[messageType]; ok {
		return info, true
	}

	typeID := NewTypeID(messageType)
	if info, ok := mr.typeInfo(typeID); ok {
		return info, true
	}

	for _, interfaceType := range mr.interfaceHandlers {
		if messageType.AssignableTo(interfaceType) {
			info := mr.typedHandlers[interfaceType]
			info = info.forRuntimeType(messageType)
			mr.typeInfos.Store(typeID, info)
			return info, true
		}
	}

	return typeHandlingInfo{}, false
}

// callResult captures a handler invocation result and error.
type callResult struct {
	result     any
	err        error
	autoOutput bool
}

// isVoid reports whether the handler returned the workflow void sentinel.
func (cr callResult) isVoid() bool {
	return cr.result != nil && reflect.TypeOf(cr.result) == reflect.TypeFor[struct{}]()
}
