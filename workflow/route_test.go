// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"reflect"
	"testing"
)

type routeTestMessage interface {
	routeMarker() string
}

type routeTestConcrete struct {
	value string
}

func (m routeTestConcrete) routeMarker() string { return m.value }

func TestMessageRouterRoutesAssignableInterfaceHandler(t *testing.T) {
	var rb RouteBuilder
	rb.AddHandlerRaw(reflect.TypeFor[routeTestMessage](), nil, func(_ *Context, msg any) (any, error) {
		message, ok := msg.(routeTestMessage)
		if !ok {
			t.Fatalf("handler message = %T, want routeTestMessage", msg)
		}
		return message.routeMarker(), nil
	})
	router, err := rb.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if !router.canHandleType(reflect.TypeFor[routeTestConcrete]()) {
		t.Fatal("CanHandleType(routeTestConcrete) = false, want true")
	}
	result, handled := router.routeMessage(&Context{Context: context.Background()}, routeTestConcrete{value: "handled"})
	if !handled {
		t.Fatal("RouteMessage handled = false, want true")
	}
	if result.err != nil {
		t.Fatalf("RouteMessage error = %v", result.err)
	}
	if result.result != "handled" {
		t.Fatalf("RouteMessage result = %v, want handled", result.result)
	}
}

func TestMessageRouterPrefersExactHandlerOverInterfaceHandler(t *testing.T) {
	var rb RouteBuilder
	rb.AddHandlerRaw(reflect.TypeFor[routeTestMessage](), nil, func(_ *Context, msg any) (any, error) {
		return "interface", nil
	})
	rb.AddHandlerRaw(reflect.TypeFor[routeTestConcrete](), nil, func(_ *Context, msg any) (any, error) {
		return "concrete", nil
	})
	router, err := rb.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	result, handled := router.routeMessage(&Context{Context: context.Background()}, routeTestConcrete{})
	if !handled {
		t.Fatal("RouteMessage handled = false, want true")
	}
	if result.err != nil {
		t.Fatalf("RouteMessage error = %v", result.err)
	}
	if result.result != "concrete" {
		t.Fatalf("RouteMessage result = %v, want concrete", result.result)
	}
}

func TestMessageRouterRoutesPortableValueAfterInterfaceMatchIsCached(t *testing.T) {
	var rb RouteBuilder
	rb.AddHandlerRaw(reflect.TypeFor[routeTestMessage](), nil, func(_ *Context, msg any) (any, error) {
		if _, ok := msg.(PortableValue); ok {
			t.Fatalf("handler message = %T, want unwrapped concrete value", msg)
		}
		message, ok := msg.(routeTestMessage)
		if !ok {
			t.Fatalf("handler message = %T, want routeTestMessage", msg)
		}
		return message.routeMarker(), nil
	})
	router, err := rb.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	concreteTypeID := NewTypeID(reflect.TypeFor[routeTestConcrete]())
	if router.canHandle(concreteTypeID) {
		t.Fatal("CanHandle(routeTestConcrete TypeID) = true before cache, want false")
	}

	if _, handled := router.routeMessage(&Context{Context: context.Background()}, routeTestConcrete{value: "first"}); !handled {
		t.Fatal("RouteMessage concrete handled = false, want true")
	}
	if !router.canHandle(concreteTypeID) {
		t.Fatal("CanHandle(routeTestConcrete TypeID) = false after cache, want true")
	}
	result, handled := router.routeMessage(&Context{Context: context.Background()}, AnyPortableValue(routeTestConcrete{value: "portable"}))
	if !handled {
		t.Fatal("RouteMessage portable handled = false, want true")
	}
	if result.err != nil {
		t.Fatalf("RouteMessage portable error = %v", result.err)
	}
	if result.result != "portable" {
		t.Fatalf("RouteMessage portable result = %v, want portable", result.result)
	}
}
