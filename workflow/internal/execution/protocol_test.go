// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
)

func TestDeclaredSendTypeIncludesKnownSystemSendTypes(t *testing.T) {
	executor := executorWithSendTypes()
	systemTypes := []reflect.Type{
		reflect.TypeFor[*workflow.ExternalRequest](),
		reflect.TypeFor[*workflow.ExternalResponse](),
	}

	for _, systemType := range systemTypes {
		if got, ok := DeclaredSendType(executor, systemType); !ok || got != systemType {
			t.Fatalf("DeclaredSendType(%v) = %v, %v; want %v, true", systemType, got, ok, systemType)
		}
	}

	protocol := executor.DescribeProtocol()
	for _, systemType := range systemTypes {
		if slicesContains(protocol.Sends, systemType) {
			t.Fatalf("Sends = %v, want no implicit system send type %v", protocol.Sends, systemType)
		}
	}
}

func TestDeclaredSendTypeTightensInterfaceMatching(t *testing.T) {
	concreteType := reflect.TypeFor[protocolSendConcrete]()
	interfaceType := reflect.TypeFor[protocolSendInterface]()
	anyType := reflect.TypeFor[any]()

	exactExecutor := executorWithSendTypes(concreteType)
	if got, ok := DeclaredSendType(exactExecutor, concreteType); !ok || got != concreteType {
		t.Fatalf("DeclaredSendType(concrete) = %v, %v; want %v, true", got, ok, concreteType)
	}

	interfaceExecutor := executorWithSendTypes(interfaceType)
	if got, ok := DeclaredSendType(interfaceExecutor, concreteType); ok {
		t.Fatalf("DeclaredSendType(concrete with interface declaration) = %v, true; want false", got)
	}
	if got, ok := DeclaredSendType(interfaceExecutor, interfaceType); !ok || got != interfaceType {
		t.Fatalf("DeclaredSendType(interface) = %v, %v; want %v, true", got, ok, interfaceType)
	}

	anyExecutor := executorWithSendTypes(anyType)
	if got, ok := DeclaredSendType(anyExecutor, concreteType); !ok || got != anyType {
		t.Fatalf("DeclaredSendType(concrete with any declaration) = %v, %v; want %v, true", got, ok, anyType)
	}
}

func TestDeclaredSendTypePrefersExactDeclarationOverAny(t *testing.T) {
	concreteType := reflect.TypeFor[protocolSendConcrete]()
	executor := executorWithSendTypes(reflect.TypeFor[any](), concreteType)

	got, ok := DeclaredSendType(executor, concreteType)
	if !ok || got != concreteType {
		t.Fatalf("DeclaredSendType(concrete with any and concrete declarations) = %v, %v; want %v, true", got, ok, concreteType)
	}
}

func TestSentRuntimeTypeIncludesDeclaredAndKnownSendTypes(t *testing.T) {
	concreteType := reflect.TypeFor[protocolSendConcrete]()
	executor := executorWithSendTypes(concreteType)

	for _, typ := range []reflect.Type{concreteType, reflect.TypeFor[*workflow.ExternalRequest](), reflect.TypeFor[*workflow.ExternalResponse]()} {
		got, ok := SentRuntimeType(executor, workflow.NewTypeID(typ))
		if !ok || got != typ {
			t.Fatalf("SentRuntimeType(%v) = %v, %v; want %v, true", typ, got, ok, typ)
		}
	}
}

func TestCanHandleTypeUsesProtocolDescriptorPolymorphicMatch(t *testing.T) {
	interfaceType := reflect.TypeFor[protocolInputInterface]()
	executor := executorWithInputTypes(interfaceType)

	if !CanHandleType(executor, reflect.TypeFor[*protocolInputImpl]()) {
		t.Fatal("interface input should polymorphically match implementing pointer input")
	}
	if !CanHandleTypeID(executor, workflow.NewTypeID(interfaceType)) {
		t.Fatal("interface TypeID should match declared input")
	}
	if CanHandleType(executor, reflect.TypeFor[protocolSendConcrete]()) {
		t.Fatal("unrelated input type should not match declared inputs")
	}
	if CanHandleType(executor, nil) {
		t.Fatal("nil input type should not match declared inputs")
	}
}

func TestCanHandleTypeUsesCatchAllDescriptor(t *testing.T) {
	executor := &workflow.Executor{
		ID: "catch-all",
		Spec: workflow.ExecutorSpec{
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.RouteBuilder.AddCatchAll(func(*workflow.Context, workflow.PortableValue) (any, error) {
					return nil, nil
				})
				return rb, nil
			},
		},
	}

	if !CanHandleType(executor, reflect.TypeFor[protocolSendConcrete]()) {
		t.Fatal("catch-all should match runtime type")
	}
	if !CanHandleTypeID(executor, workflow.NewTypeID(reflect.TypeFor[protocolSendConcrete]())) {
		t.Fatal("catch-all should match TypeID")
	}
}

func TestCanOutputTypeUsesProtocolDescriptorPolymorphicMatch(t *testing.T) {
	executor := executorWithYieldTypes(
		reflect.TypeFor[protocolYield](),
		reflect.TypeFor[protocolYieldInterface](),
	)

	if !CanOutputType(executor, reflect.TypeFor[*protocolYield]()) {
		t.Fatal("value TypeID yield should match pointer output")
	}
	if !CanOutputType(executor, reflect.TypeFor[*protocolYieldImpl]()) {
		t.Fatal("interface TypeID yield should polymorphically match implementing pointer output")
	}
	if CanOutputType(executor, reflect.TypeFor[protocolSendConcrete]()) {
		t.Fatal("unrelated output type should not match declared yields")
	}
	if CanOutputType(executor, nil) {
		t.Fatal("nil output type should not match declared yields")
	}
}

type protocolSendInterface interface {
	ProtocolSendMarker()
}

type protocolSendConcrete struct{}

func (protocolSendConcrete) ProtocolSendMarker() {}

type protocolInputInterface interface {
	ProtocolInputMarker()
}

type protocolInputImpl struct{}

func (*protocolInputImpl) ProtocolInputMarker() {}

type protocolYield struct{}

type protocolYieldInterface interface {
	ProtocolYieldMarker()
}

type protocolYieldImpl struct{}

func (*protocolYieldImpl) ProtocolYieldMarker() {}

func executorWithSendTypes(sendTypes ...reflect.Type) *workflow.Executor {
	return &workflow.Executor{
		ID: "send-protocol",
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.SendsMessageType(sendTypes...)
				rb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[string](), nil, func(*workflow.Context, any) (any, error) {
					return nil, nil
				})
				return rb, nil
			},
		},
	}
}

func executorWithInputTypes(inputTypes ...reflect.Type) *workflow.Executor {
	return &workflow.Executor{
		ID: "input-protocol",
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				for _, inputType := range inputTypes {
					rb.RouteBuilder.AddHandlerRaw(inputType, nil, func(*workflow.Context, any) (any, error) {
						return nil, nil
					})
				}
				return rb, nil
			},
		},
	}
}

func executorWithYieldTypes(yieldTypes ...reflect.Type) *workflow.Executor {
	return &workflow.Executor{
		ID: "yield-protocol",
		Spec: workflow.ExecutorSpec{
			DisableAutoSendMessageHandlerResultObject: true,
			DisableAutoYieldOutputHandlerResultObject: true,
			ConfigureProtocol: func(rb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
				rb.YieldsOutputType(yieldTypes...)
				return rb, nil
			},
		},
	}
}

func slicesContains(types []reflect.Type, typ reflect.Type) bool {
	for _, candidate := range types {
		if candidate == typ {
			return true
		}
	}
	return false
}
