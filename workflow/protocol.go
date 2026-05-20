// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"errors"
	"reflect"
	"slices"
)

// ProtocolBuilder configures the routes and declared message protocol for an
// executor.
//
// A ProtocolBuilder owns a [RouteBuilder] for message handlers and tracks the
// message types that may be sent with [Context.SendMessage] or yielded with
// [Context.YieldOutput]. The zero value is ready to use.
type ProtocolBuilder struct {
	RouteBuilder RouteBuilder

	sendTypes  []reflect.Type
	yieldTypes []reflect.Type
	err        error
}

func (pb *ProtocolBuilder) routeBuilder() *RouteBuilder {
	return &pb.RouteBuilder
}

// SendsMessageType adds messageTypes to the set of declared message types this
// executor may send with [Context.SendMessage]. Nil and duplicate types are
// ignored.
func (pb *ProtocolBuilder) SendsMessageType(messageTypes ...reflect.Type) *ProtocolBuilder {
	if pb == nil {
		panic("workflow: cannot configure nil ProtocolBuilder")
	}
	pb.sendTypes = appendUniqueTypes(pb.sendTypes, messageTypes...)
	return pb
}

// YieldsOutputType adds outputTypes to the set of declared output types this
// executor may yield with [Context.YieldOutput]. Nil and duplicate types are
// ignored.
func (pb *ProtocolBuilder) YieldsOutputType(outputTypes ...reflect.Type) *ProtocolBuilder {
	if pb == nil {
		panic("workflow: cannot configure nil ProtocolBuilder")
	}
	pb.yieldTypes = appendUniqueTypes(pb.yieldTypes, outputTypes...)
	return pb
}

// ConfigureRoutes fluently configures message handlers on this builder's route
// builder.
//
// Any error returned from configure is recorded and returned later when the
// protocol is built.
func (pb *ProtocolBuilder) ConfigureRoutes(configure func(*RouteBuilder) (*RouteBuilder, error)) *ProtocolBuilder {
	if configure == nil {
		panic("workflow: route configure function cannot be nil")
	}
	if pb == nil {
		panic("workflow: cannot configure nil ProtocolBuilder")
	}
	if pb.err != nil {
		return pb
	}
	routeBuilder, err := configure(pb.routeBuilder())
	if err != nil {
		pb.err = err
		return pb
	}
	if routeBuilder == nil {
		pb.err = errors.New("workflow: route configure function returned nil RouteBuilder")
		return pb
	}
	pb.RouteBuilder = *routeBuilder
	return pb
}

func (pb *ProtocolBuilder) build(spec ExecutorSpec) (*executorProtocol, error) {
	if pb == nil {
		return nil, errors.New("workflow: cannot build nil ProtocolBuilder")
	}
	if pb.err != nil {
		return nil, pb.err
	}
	router, err := pb.routeBuilder().build()
	if err != nil {
		return nil, err
	}

	sendTypes := appendUniqueTypes(nil, pb.sendTypes...)
	if !spec.DisableAutoSendMessageHandlerResultObject {
		sendTypes = appendUniqueTypes(sendTypes, slices.Collect(router.defaultOutputTypes())...)
	}

	yieldTypes := appendUniqueTypes(nil, pb.yieldTypes...)
	if !spec.DisableAutoYieldOutputHandlerResultObject {
		yieldTypes = appendUniqueTypes(yieldTypes, slices.Collect(router.defaultOutputTypes())...)
	}

	return &executorProtocol{
		router: router,
		sends:  sendTypes,
		yields: yieldTypes,
	}, nil
}

type executorProtocol struct {
	router *messageRouter
	sends  []reflect.Type
	yields []reflect.Type
}

func (p *executorProtocol) describe() *ProtocolDescriptor {
	return &ProtocolDescriptor{
		Accepts:    p.router.incomingTypes(),
		Yields:     appendUniqueTypes(nil, p.yields...),
		Sends:      appendUniqueTypes(nil, p.sends...),
		AcceptsAll: p.router.hasCatchAll(),
	}
}

func (p *executorProtocol) canHandleTypeID(typ TypeID) bool {
	return p.router.canHandle(typ)
}

func (p *executorProtocol) canHandleType(typ reflect.Type) bool {
	return p.router.canHandleType(typ)
}

func (p *executorProtocol) canOutputType(typ reflect.Type) bool {
	return slices.ContainsFunc(p.yields, typ.AssignableTo)
}

func (p *executorProtocol) declaredSendType(typ reflect.Type) (reflect.Type, bool) {
	candidates := appendUniqueTypes(nil, p.sends...)
	candidates = appendUniqueTypes(candidates, knownSentTypes()...)
	for _, candidate := range candidates {
		if declaredSendTypeMatches(typ, candidate) {
			return candidate, true
		}
	}
	return nil, false
}
