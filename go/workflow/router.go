// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"reflect"
)

type CatchAllFuncFunc func(ctx context.Context, wctx Context, msg Value) CallResult

type MessageHandlerFunc func(ctx context.Context, wctx Context, msg any) CallResult

type MessageRouter struct {
	handlers           map[reflect.Type]MessageHandlerFunc
	catchAllFunc       CatchAllFuncFunc
	defaultOutputTypes map[reflect.Type]struct{}
}

func NewMessageRouter(handlers map[reflect.Type]MessageHandlerFunc, outputTypes map[reflect.Type]struct{}, catchAll CatchAllFuncFunc) *MessageRouter {
	if handlers == nil {
		panic("nil handlers map")
	}
	return &MessageRouter{
		handlers:           handlers,
		catchAllFunc:       catchAll,
		defaultOutputTypes: outputTypes,
	}
}

func (mr *MessageRouter) IncomingTypes() iter.Seq[reflect.Type] {
	return maps.Keys(mr.handlers)
}

func (mr *MessageRouter) DefaultOutputTypes() iter.Seq[reflect.Type] {
	return maps.Keys(mr.defaultOutputTypes)
}

func (mr *MessageRouter) CanHandle(typ reflect.Type) bool {
	if mr.catchAllFunc != nil {
		return true
	}
	_, ok := mr.handlers[typ]
	return ok
}

func (mr *MessageRouter) RouteMessage(ctx context.Context, wctx Context, msg any) (CallResult, bool) {
	if msg == nil {
		panic("nil message")
	}
	if handler, ok := mr.handlers[reflect.TypeOf(msg)]; ok {
		return handler(ctx, wctx, msg), true
	}
	if mr.catchAllFunc != nil {
		return mr.catchAllFunc(ctx, wctx, AnyValue(msg)), true
	}
	return CallResult{}, false
}

type RouteBuilder struct {
	handlers    map[reflect.Type]MessageHandlerFunc
	outputTypes map[reflect.Type]reflect.Type
	catchAll    CatchAllFuncFunc
	err         error

	built  bool
	cached *MessageRouter
}

func (rb *RouteBuilder) Cached() (*MessageRouter, error, bool) {
	return rb.cached, rb.err, rb.built
}

func (rb *RouteBuilder) AddHandler(messageType reflect.Type, outputType reflect.Type, overwrite bool, handler MessageHandlerFunc) *RouteBuilder {
	if messageType == nil {
		panic("messageType cannot be nil")
	}
	if handler == nil {
		panic("handler cannot be nil")
	}
	if rb.err != nil {
		return rb
	}
	if reflect.TypeOf(messageType) == reflect.TypeFor[Value]() {
		rb.err = errors.New("cannot register a handler for PortableValue. Use AddCatchAll() instead")
		return rb
	}
	// Overwrite must be false if the type is not registered. Overwrite must be true if the type is registered.
	if _, exists := rb.handlers[messageType]; exists == overwrite {
		rb.handlers[messageType] = handler
		if outputType != nil {
			rb.outputTypes[messageType] = outputType
		} else {
			delete(rb.outputTypes, messageType)
		}
	} else if overwrite {
		// overwrite is true, but the type is not registered.
		rb.err = fmt.Errorf("cannot overwrite handler for unregistered type %s", messageType)
		return rb
	} else if !overwrite {
		// overwrite is false, but the type is already registered.
		rb.err = fmt.Errorf("handler for type %s is already registered", messageType)
		return rb
	}
	return rb
}

func (rb *RouteBuilder) AddCatchAll(overwrite bool, handler CatchAllFuncFunc) *RouteBuilder {
	if handler == nil {
		panic("handler cannot be nil")
	}
	if rb.err != nil {
		return rb
	}
	if rb.catchAll != nil && !overwrite {
		rb.err = errors.New("catch-all handler is already registered")
		return rb
	}
	rb.catchAll = handler
	return rb
}

func (rb *RouteBuilder) Build() (*MessageRouter, error) {
	rb.built = true
	if rb.err != nil {
		return nil, rb.err
	}
	defaultOutputTypes := make(map[reflect.Type]struct{}, len(rb.outputTypes))
	for _, outType := range rb.outputTypes {
		defaultOutputTypes[outType] = struct{}{}
	}
	router := NewMessageRouter(rb.handlers, defaultOutputTypes, rb.catchAll)
	return router, nil
}
