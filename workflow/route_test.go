// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
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

type reflectedPortDefault struct{ invoked bool }

func (handler *reflectedPortDefault) Handle(any) { handler.invoked = true }

type reflectedPortVoid struct{ invoked bool }

func (handler *reflectedPortVoid) Handle(int) { handler.invoked = true }

type reflectedPortValue struct{ invoked bool }

func (handler *reflectedPortValue) Handle(value int) string {
	handler.invoked = true
	return fmt.Sprint(value)
}

func TestReflectingExecutor_DefaultHandler(t *testing.T) {
	handler := &reflectedPortDefault{}
	router, err := NewExecutor("DefaultHandler", handler).router()
	if err != nil {
		t.Fatal(err)
	}
	input := new(struct{})
	if router == nil || !router.canHandleType(reflect.TypeOf(input)) {
		t.Fatal("default handler cannot handle object input")
	}
	result, handled := router.routeMessage(&Context{Context: t.Context()}, input)
	if !handled || !handler.invoked || result.err != nil || result.result != nil || len(slices.Collect(router.defaultOutputTypes())) != 0 {
		t.Fatalf("reflection result = %#v, %t", result, handled)
	}
}

func TestReflectingExecutor_TypedVoidHandler(t *testing.T) {
	handler := &reflectedPortVoid{}
	router, err := NewExecutor("TypedHandler", handler).router()
	if err != nil {
		t.Fatal(err)
	}
	if router == nil || !router.canHandleType(reflect.TypeFor[int]()) {
		t.Fatal("typed handler cannot handle int input")
	}
	result, handled := router.routeMessage(&Context{Context: t.Context()}, 3)
	if !handled || !handler.invoked || result.err != nil || result.result != nil || len(slices.Collect(router.defaultOutputTypes())) != 0 {
		t.Fatalf("reflection result = %#v, %t", result, handled)
	}
}

func TestReflectingExecutor_TypedValueHandler(t *testing.T) {
	handler := &reflectedPortValue{}
	router, err := NewExecutor("TypedHandlerWithOutput", handler).router()
	if err != nil {
		t.Fatal(err)
	}
	if router == nil || !router.canHandleType(reflect.TypeFor[int]()) {
		t.Fatal("typed handler cannot handle int input")
	}
	result, handled := router.routeMessage(&Context{Context: t.Context()}, 3)
	if !handled || !handler.invoked || result.err != nil || result.isVoid() || result.result != "3" {
		t.Fatalf("reflection result = %#v, %t, want successful nonvoid 3", result, handled)
	}
}

func TestRouteBuilder_RejectsDuplicateTypedHandler(t *testing.T) {
	var builder RouteBuilder
	handler := func(*Context, any) (any, error) { return nil, nil }
	builder.AddHandlerRaw(reflect.TypeFor[string](), nil, handler)
	builder.AddHandlerRaw(reflect.TypeFor[string](), nil, handler)
	if _, err := builder.build(); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate handler error = %v", err)
	}
}

func TestRouteBuilder_RejectsDuplicateCatchAll(t *testing.T) {
	var builder RouteBuilder
	handler := func(*Context, PortableValue) (any, error) { return nil, nil }
	builder.AddCatchAll(handler)
	builder.AddCatchAll(handler)
	if _, err := builder.build(); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate catch-all error = %v", err)
	}
}

func TestRouteBuilder_OverwritesTypedHandler(t *testing.T) {
	var builder RouteBuilder
	builder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *Context, _ any) (any, error) { return nil, ctx.SendMessage("", "first") })
	builder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *Context, _ any) (any, error) { return nil, ctx.SendMessage("", "second") }, WithHandlerOverwrite(true))
	router, err := builder.build()
	if err != nil {
		t.Fatal(err)
	}
	var sent []any
	ctx := &Context{Context: t.Context(), SendMessage: func(_ string, value any) error { sent = append(sent, value); return nil }}
	if result, handled := router.routeMessage(ctx, "hello"); !handled || result.err != nil {
		t.Fatalf("route = %#v, %t", result, handled)
	}
	if !reflect.DeepEqual(sent, []any{"second"}) {
		t.Fatalf("sent = %v, want [second]", sent)
	}
}

func TestRouteBuilder_OverwritesCatchAll(t *testing.T) {
	var builder RouteBuilder
	builder.AddCatchAll(func(ctx *Context, _ PortableValue) (any, error) { return nil, ctx.SendMessage("", "first") })
	builder.AddCatchAll(func(ctx *Context, _ PortableValue) (any, error) { return nil, ctx.SendMessage("", "second") }, WithHandlerOverwrite(true))
	router, err := builder.build()
	if err != nil {
		t.Fatal(err)
	}
	var sent []any
	ctx := &Context{Context: t.Context(), SendMessage: func(_ string, value any) error { sent = append(sent, value); return nil }}
	if result, handled := router.routeMessage(ctx, routeTestConcrete{value: "hello"}); !handled || result.err != nil {
		t.Fatalf("route = %#v, %t", result, handled)
	}
	if !reflect.DeepEqual(sent, []any{"second"}) {
		t.Fatalf("sent = %v, want [second]", sent)
	}
}

func TestRouteBuilder_UntypedVoidAndResultHandlers(t *testing.T) {
	type capture struct {
		message any
		ctx     *Context
		token   context.Context
	}
	var builder RouteBuilder
	var voidCall, resultCall capture
	builder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(ctx *Context, value any) (any, error) {
		voidCall = capture{value, ctx, ctx.Context}
		return struct{}{}, nil
	})
	builder.AddHandlerRaw(reflect.TypeFor[int](), reflect.TypeFor[int](), func(ctx *Context, value any) (any, error) {
		resultCall = capture{value, ctx, ctx.Context}
		return value.(int) + 1, nil
	})
	router, err := builder.build()
	if err != nil {
		t.Fatal(err)
	}
	token, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &Context{Context: token}
	voidResult, handled := router.routeMessage(ctx, "hello")
	if !handled || !voidResult.isVoid() || voidCall.message != "hello" || voidCall.ctx != ctx || voidCall.token != token {
		t.Fatalf("void result/call = %#v/%#v", voidResult, voidCall)
	}
	result, handled := router.routeMessage(ctx, 41)
	if !handled || result.result != 42 || resultCall.message != 41 || resultCall.ctx != ctx || resultCall.token != token {
		t.Fatalf("typed result/call = %#v/%#v", result, resultCall)
	}
	if !slices.Contains(slices.Collect(router.defaultOutputTypes()), reflect.TypeFor[int]()) {
		t.Fatal("router output types do not contain int")
	}
}

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
	if !strings.Contains(err.Error(), "Use AddCatchAll()") {
		t.Fatalf("build() error = %q, want Use AddCatchAll() guidance", err)
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
	var caught PortableValue
	rb.AddCatchAll(func(_ *Context, msg PortableValue) (any, error) {
		caught = msg
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
	if caught.Delayed() {
		t.Fatal("catch-all message remains delayed after routing")
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
