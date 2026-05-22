// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"errors"
	"reflect"
	"testing"
)

type (
	protocolBuilderInput  struct{}
	protocolBuilderOutput struct{}
	protocolBuilderSend   struct{}
	protocolBuilderYield  struct{}
)

func TestProtocolBuilderBuildIncludesDeclaredAndAutomaticTypes(t *testing.T) {
	inputType := reflect.TypeFor[protocolBuilderInput]()
	outputType := reflect.TypeFor[protocolBuilderOutput]()
	explicitSend := reflect.TypeFor[protocolBuilderSend]()
	explicitYield := reflect.TypeFor[protocolBuilderYield]()

	protocol, err := newProtocolBuilderWithHandler(inputType, outputType).
		SendsMessageType(explicitSend, nil, explicitSend).
		YieldsOutputType(explicitYield, nil, explicitYield).
		build(ExecutorSpec{})
	if err != nil {
		t.Fatalf("build error = %v", err)
	}

	if !containsReflectType(protocol.describe().Accepts, inputType) {
		t.Fatalf("Accepts = %v, want %v", protocol.describe().Accepts, inputType)
	}
	if got := protocol.describe().Sends; !reflect.DeepEqual(got, []reflect.Type{explicitSend, outputType}) {
		t.Fatalf("sends = %v, want [%v %v]", got, explicitSend, outputType)
	}
	if got := protocol.describe().Yields; !reflect.DeepEqual(got, []reflect.Type{explicitYield, outputType}) {
		t.Fatalf("yields = %v, want [%v %v]", got, explicitYield, outputType)
	}
}

func TestExecutorProtocolDescribeReturnsCachedDescriptorValue(t *testing.T) {
	protocol, err := newProtocolBuilderWithHandler(reflect.TypeFor[protocolBuilderInput](), reflect.TypeFor[protocolBuilderOutput]()).
		SendsMessageType(reflect.TypeFor[protocolBuilderSend]()).
		YieldsOutputType(reflect.TypeFor[protocolBuilderYield]()).
		build(ExecutorSpec{})
	if err != nil {
		t.Fatalf("build error = %v", err)
	}

	first := protocol.describe()
	second := protocol.describe()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("describe returned different descriptors: %+v != %+v", first, second)
	}
	if allocations := testing.AllocsPerRun(1000, func() { _ = protocol.describe() }); allocations != 0 {
		t.Fatalf("describe allocations = %v, want 0", allocations)
	}
}

func TestProtocolBuilderBuildRespectsAutoReturnOptions(t *testing.T) {
	inputType := reflect.TypeFor[protocolBuilderInput]()
	outputType := reflect.TypeFor[protocolBuilderOutput]()

	protocol, err := newProtocolBuilderWithHandler(inputType, outputType).build(ExecutorSpec{
		DisableAutoSendMessageHandlerResultObject: true,
		DisableAutoYieldOutputHandlerResultObject: true,
	})
	if err != nil {
		t.Fatalf("build error = %v", err)
	}
	if containsReflectType(protocol.describe().Sends, outputType) {
		t.Fatalf("sends = %v, want no automatic output type %v", protocol.describe().Sends, outputType)
	}
	if containsReflectType(protocol.describe().Yields, outputType) {
		t.Fatalf("yields = %v, want no automatic output type %v", protocol.describe().Yields, outputType)
	}
}

func TestProtocolBuilderConfigureRoutesErrorReturnsFromBuild(t *testing.T) {
	wantErr := errors.New("route setup failed")
	var pb ProtocolBuilder
	_, err := pb.ConfigureRoutes(func(*RouteBuilder) (*RouteBuilder, error) {
		return nil, wantErr
	}).build(ExecutorSpec{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("build error = %v, want %v", err, wantErr)
	}
}

func newProtocolBuilderWithHandler(inputType, outputType reflect.Type) *ProtocolBuilder {
	var pb ProtocolBuilder
	pb.RouteBuilder.AddHandlerRaw(inputType, outputType, func(*Context, any) (any, error) {
		return protocolBuilderOutput{}, nil
	})
	return &pb
}

func containsReflectType(types []reflect.Type, want reflect.Type) bool {
	for _, typ := range types {
		if typ == want {
			return true
		}
	}
	return false
}
