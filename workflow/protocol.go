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

func (pb *ProtocolBuilder) build(executor *Executor) (*executorProtocol, error) {
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
	if !executor.DisableAutoSendMessageHandlerResultObject {
		sendTypes = appendUniqueTypes(sendTypes, slices.Collect(router.defaultOutputTypes())...)
	}
	yieldTypes := appendUniqueTypes(nil, pb.yieldTypes...)
	if !executor.DisableAutoYieldOutputHandlerResultObject {
		yieldTypes = appendUniqueTypes(yieldTypes, slices.Collect(router.defaultOutputTypes())...)
	}
	descriptor := ProtocolDescriptor{
		Accepts:    router.incomingTypes(),
		Yields:     slices.Clone(yieldTypes),
		Sends:      slices.Clone(sendTypes),
		AcceptsAll: router.hasCatchAll(),
	}

	return &executorProtocol{
		router:     router,
		descriptor: descriptor,
	}, nil
}

type executorProtocol struct {
	router     *messageRouter
	descriptor ProtocolDescriptor
}

func (p *executorProtocol) describe() ProtocolDescriptor {
	return p.descriptor
}
