// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"errors"
	"fmt"
	"iter"
	"maps"
	"reflect"
	"slices"
)

type CatchAllFunc func(*Context, PortableValue) (any, error)

type MessageHandlerFunc func(*Context, any) (any, error)

type RouteBuilder struct {
	handlers    map[reflect.Type]MessageHandlerFunc
	outputTypes map[reflect.Type]reflect.Type
	catchAll    CatchAllFunc
	err         error

	built bool
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
	if reflect.TypeOf(messageType) == reflect.TypeFor[PortableValue]() {
		rb.err = errors.New("cannot register a handler for PortableValue. Use AddCatchAll() instead")
		return rb
	}
	// Overwrite must be false if the type is not registered. Overwrite must be true if the type is registered.
	if _, exists := rb.handlers[messageType]; exists == overwrite {
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

func (rb *RouteBuilder) AddCatchAll(overwrite bool, handler func(*Context, PortableValue) (any, error)) *RouteBuilder {
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

type messageRouter struct {
	typedHandlers      map[reflect.Type]MessageHandlerFunc
	runtimeTypeMap     map[TypeID]reflect.Type
	catchAllFunc       CatchAllFunc
	defaultOutputTypes map[reflect.Type]struct{}
}

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

func (mr *messageRouter) IncomingTypes() []reflect.Type {
	return slices.Collect(maps.Keys(mr.typedHandlers))
}

func (mr *messageRouter) DefaultOutputTypes() iter.Seq[reflect.Type] {
	return maps.Keys(mr.defaultOutputTypes)
}

func (mr *messageRouter) CanHandle(typ TypeID) bool {
	if mr.catchAllFunc != nil {
		return true
	}
	_, ok := mr.runtimeTypeMap[typ]
	return ok
}

func (mr *messageRouter) RouteMessage(ctx *Context, msg any) (result callResult, handled bool) {
	if msg == nil {
		panic("nil message")
	}
	pvalue, ok := msg.(PortableValue)
	if ok {
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
		ret, err := handler(ctx, msg)
		return callResult{ret, err}, true
	}
	if mr.catchAllFunc != nil {
		if pvalue.IsZero() {
			pvalue = AnyPortableValue(msg)
		}
		ret, err := mr.catchAllFunc(ctx, pvalue)
		return callResult{ret, err}, true
	}
	return callResult{}, false
}
