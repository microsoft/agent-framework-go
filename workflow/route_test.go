// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type routeTestMessage interface {
	routeMarker() string
}

type routeTestConcrete struct {
	value string
}

func (m routeTestConcrete) routeMarker() string { return m.value }

func TestRouteBuilder_OverwriteRequiresRegisteredHandler(t *testing.T) {
	tests := []struct {
		name string
		add  func(*RouteBuilder)
		want string
	}{
		{
			name: "typed handler",
			add: func(rb *RouteBuilder) {
				rb.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*Context, any) (any, error) {
					return nil, nil
				}, WithHandlerOverwrite(true))
			},
			want: "cannot overwrite handler for unregistered type string",
		},
		{
			name: "catch-all handler",
			add: func(rb *RouteBuilder) {
				rb.AddCatchAll(func(*Context, PortableValue) (any, error) {
					return nil, nil
				}, WithHandlerOverwrite(true))
			},
			want: "cannot overwrite unregistered catch-all handler",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var rb RouteBuilder
			test.add(&rb)
			_, err := rb.build()
			if err == nil || err.Error() != test.want {
				t.Fatalf("build() error = %v, want %q", err, test.want)
			}
		})
	}
}

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

func TestAddHandlerRawRejectsPortableValue(t *testing.T) {
	var rb RouteBuilder
	rb.AddHandlerRaw(reflect.TypeFor[PortableValue](), nil, func(_ *Context, _ any) (any, error) {
		return nil, nil
	})
	_, err := rb.build()
	if err == nil {
		t.Fatal("build() error = nil, want error rejecting a PortableValue handler")
	}
	if !strings.Contains(err.Error(), "PortableValue") {
		t.Fatalf("build() error = %q, want mention of PortableValue", err)
	}
}

func TestMessageRouterKeepsUnknownPortableTypeOnCatchAllPath(t *testing.T) {
	var portable PortableValue
	if err := json.Unmarshal([]byte(`{"TypeID":{"PackageName":"example.invalid/missing","TypeName":"Payload"},"Value":{"value":"test"}}`), &portable); err != nil {
		t.Fatal(err)
	}

	var rb RouteBuilder
	rb.AddHandlerRaw(reflect.TypeFor[map[string]any](), nil, func(*Context, any) (any, error) {
		return "map", nil
	})
	rb.AddCatchAll(func(*Context, PortableValue) (any, error) {
		return "catch-all", nil
	})
	router, err := rb.build()
	if err != nil {
		t.Fatal(err)
	}

	result, handled := router.routeMessage(&Context{Context: context.Background()}, portable)
	if !handled || result.err != nil {
		t.Fatalf("RouteMessage() = (%v, %v), want handled without error", handled, result.err)
	}
	if result.result != "catch-all" {
		t.Fatalf("RouteMessage() result = %v, want catch-all", result.result)
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

func TestMessageRouterCachedInterfaceHandlerPreservesAutoOutput(t *testing.T) {
	var rb RouteBuilder
	rb.AddHandlerRaw(reflect.TypeFor[routeTestMessage](), reflect.TypeFor[string](), func(_ *Context, msg any) (any, error) {
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

	if _, handled := router.routeMessage(&Context{Context: context.Background()}, routeTestConcrete{value: "first"}); !handled {
		t.Fatal("RouteMessage concrete handled = false, want true")
	}
	result, handled := router.routeMessage(&Context{Context: context.Background()}, AnyPortableValue(routeTestConcrete{value: "portable"}))
	if !handled {
		t.Fatal("RouteMessage portable handled = false, want true")
	}
	if result.err != nil {
		t.Fatalf("RouteMessage portable error = %v", result.err)
	}
	if !result.autoOutput {
		t.Fatal("RouteMessage portable autoOutput = false, want true")
	}
	if result.result != "portable" {
		t.Fatalf("RouteMessage portable result = %v, want portable", result.result)
	}
}
