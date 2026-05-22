// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

type typeIDPayload struct {
	Value string `json:"value"`
}

type typeIDPayloadAlias = typeIDPayload

type typeIDOtherPayload struct {
	Value string `json:"value"`
}

type typeIDNamedPointer *typeIDPayload

type typeIDRecursivePointer *typeIDRecursivePointer

type (
	typeIDMutualPointerA *typeIDMutualPointerB
	typeIDMutualPointerB *typeIDMutualPointerA
)

type typeIDNamedInt int

type typeIDOtherNamedInt int

type RequestPort struct{}

type typeIDPointerOnlyMarker interface {
	pointerOnlyMarker()
}

type typeIDPointerOnlyValue struct{}

func (*typeIDPointerOnlyValue) pointerOnlyMarker() {}

type typeIDValueMarker interface {
	valueMarker()
}

type typeIDValueMarkerValue struct{}

func (typeIDValueMarkerValue) valueMarker() {}

type typeIDCustomError struct{}

func (*typeIDCustomError) Error() string { return "type id error" }

func TestTypeIDNewTypeIDIdentifiesNamedAndBuiltinTypes(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{name: "builtin int", typ: reflect.TypeFor[int]()},
		{name: "builtin string", typ: reflect.TypeFor[string]()},
		{name: "unsafe pointer", typ: reflect.TypeFor[unsafe.Pointer]()},
		{name: "workflow named type", typ: reflect.TypeFor[workflow.RequestPort]()},
		{name: "local named struct", typ: reflect.TypeFor[typeIDPayload]()},
		{name: "local named int", typ: reflect.TypeFor[typeIDNamedInt]()},
		{name: "local alias", typ: reflect.TypeFor[typeIDPayloadAlias]()},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			id := workflow.NewTypeID(testCase.typ)
			want := expectedTypeID(testCase.typ)
			if id != want {
				t.Fatalf("NewTypeID(%v) = %+v, want %+v", testCase.typ, id, want)
			}
			if !id.Match(testCase.typ) {
				t.Fatalf("%v should match its source type %v", id, testCase.typ)
			}
		})
	}
}

func TestTypeIDNewTypeIDIdentifiesUnnamedTypesByString(t *testing.T) {
	anonymousStruct := reflect.TypeOf(struct {
		Name string `json:"name"`
		Next *int   `json:"next,omitempty"`
	}{})
	cases := []reflect.Type{
		reflect.TypeFor[[]string](),
		reflect.TypeFor[[3]*int](),
		reflect.TypeFor[map[string][]*workflow.Executor](),
		reflect.TypeFor[chan<- string](),
		reflect.TypeFor[func(int, ...string) (bool, error)](),
		anonymousStruct,
		reflect.PointerTo(anonymousStruct),
	}

	for _, typ := range cases {
		t.Run(typ.String(), func(t *testing.T) {
			id := workflow.NewTypeID(typ)
			want := expectedTypeID(typ)
			if id != want {
				t.Fatalf("NewTypeID(%v) = %+v, want %+v", typ, id, want)
			}
			if id.PackageName != "" {
				t.Fatalf("PackageName = %q, want empty for unnamed type %v", id.PackageName, typ)
			}
			if id.TypeName != dereferenceType(typ).String() {
				t.Fatalf("TypeName = %q, want %q", id.TypeName, dereferenceType(typ).String())
			}
			if id.String() != id.TypeName {
				t.Fatalf("String() = %q, want %q", id.String(), id.TypeName)
			}
		})
	}
}

func TestTypeIDNewTypeIDDereferencesPointersAtAnyDepth(t *testing.T) {
	valueID := workflow.NewTypeID(reflect.TypeFor[typeIDPayload]())
	cases := []reflect.Type{
		reflect.TypeFor[*typeIDPayload](),
		reflect.TypeFor[**typeIDPayload](),
		reflect.TypeFor[***typeIDPayload](),
		reflect.TypeFor[********typeIDPayload](),
		reflect.TypeFor[typeIDNamedPointer](),
		reflect.TypeFor[*typeIDNamedPointer](),
	}

	for _, typ := range cases {
		t.Run(typ.String(), func(t *testing.T) {
			id := workflow.NewTypeID(typ)
			if id != valueID {
				t.Fatalf("NewTypeID(%v) = %+v, want %+v", typ, id, valueID)
			}
			if !id.Match(typ) {
				t.Fatalf("%+v should match %v", id, typ)
			}
		})
	}

	intID := workflow.NewTypeID(reflect.TypeFor[int]())
	deepIntID := workflow.NewTypeID(reflect.TypeFor[********int]())
	if deepIntID != intID {
		t.Fatalf("NewTypeID(********int) = %+v, want %+v", deepIntID, intID)
	}
	if !intID.Match(reflect.TypeFor[********int]()) {
		t.Fatal("int TypeID should match deeply nested int pointer")
	}
}

func TestTypeIDNewTypeIDRejectsRecursivePointerTypes(t *testing.T) {
	cases := []reflect.Type{
		reflect.TypeFor[typeIDRecursivePointer](),
		reflect.TypeFor[*typeIDRecursivePointer](),
		reflect.TypeFor[typeIDMutualPointerA](),
		reflect.TypeFor[typeIDMutualPointerB](),
	}

	for _, typ := range cases {
		t.Run(typ.String(), func(t *testing.T) {
			if got := workflow.NewTypeID(typ); got != (workflow.TypeID{}) {
				t.Fatalf("NewTypeID(%v) = %+v, want zero TypeID", typ, got)
			}
			if (workflow.TypeID{}).Match(typ) {
				t.Fatalf("zero TypeID should not match recursive pointer type %v", typ)
			}
			if workflow.NewTypeID(reflect.TypeFor[typeIDPayload]()).Match(typ) {
				t.Fatalf("unrelated TypeID should not match recursive pointer type %v", typ)
			}
		})
	}
}

func TestTypeIDNewTypeIDKeepsPackageInIdentity(t *testing.T) {
	workflowRequestPortID := workflow.NewTypeID(reflect.TypeFor[workflow.RequestPort]())
	localRequestPortID := workflow.NewTypeID(reflect.TypeFor[RequestPort]())

	if workflowRequestPortID.TypeName != localRequestPortID.TypeName {
		t.Fatalf("test setup expected matching type names, got %q and %q", workflowRequestPortID.TypeName, localRequestPortID.TypeName)
	}
	if workflowRequestPortID.PackageName == localRequestPortID.PackageName {
		t.Fatalf("test setup expected different packages, both were %q", workflowRequestPortID.PackageName)
	}
	if workflowRequestPortID == localRequestPortID {
		t.Fatal("TypeID should distinguish same type name from different packages")
	}
	if workflowRequestPortID.Match(reflect.TypeFor[RequestPort]()) {
		t.Fatal("workflow RequestPort TypeID should not match local RequestPort")
	}
}

func TestTypeIDZeroAndUnknownDoNotMatch(t *testing.T) {
	zero := workflow.TypeID{}
	if got := workflow.NewTypeID(nil); got != zero {
		t.Fatalf("NewTypeID(nil) = %+v, want zero", got)
	}
	if zero.String() != "" {
		t.Fatalf("zero String() = %q, want empty", zero.String())
	}
	if zero.Match(nil) {
		t.Fatal("zero TypeID should not match nil")
	}
	if zero.Match(reflect.TypeFor[string]()) {
		t.Fatal("zero TypeID should not match concrete types")
	}
	if zero.MatchPolymorphic(reflect.TypeFor[string]()) {
		t.Fatal("zero TypeID should not polymorphically match concrete types")
	}

	unknown := workflow.TypeID{PackageName: "example.invalid/missing", TypeName: "Missing"}
	if unknown.Match(reflect.TypeFor[typeIDPayload]()) {
		t.Fatal("unknown TypeID should not match unrelated concrete type")
	}
	if unknown.MatchPolymorphic(reflect.TypeFor[typeIDPayload]()) {
		t.Fatal("unknown TypeID should not polymorphically match unrelated concrete type")
	}
	if unknown.MatchPolymorphic(nil) {
		t.Fatal("unknown TypeID should not polymorphically match nil")
	}
}

func TestTypeIDString(t *testing.T) {
	cases := []struct {
		name string
		id   workflow.TypeID
		want string
	}{
		{name: "zero", id: workflow.TypeID{}, want: ""},
		{name: "builtin", id: workflow.NewTypeID(reflect.TypeFor[int]()), want: "int"},
		{name: "unnamed", id: workflow.NewTypeID(reflect.TypeFor[map[string]int]()), want: "map[string]int"},
		{name: "workflow named", id: workflow.NewTypeID(reflect.TypeFor[workflow.RequestPort]()), want: "RequestPort, github.com/microsoft/agent-framework-go/workflow"},
		{name: "unsafe pointer", id: workflow.NewTypeID(reflect.TypeFor[unsafe.Pointer]()), want: "Pointer, unsafe"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.id.String(); got != testCase.want {
				t.Fatalf("String() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestTypeIDJSONRoundtrip(t *testing.T) {
	cases := []workflow.TypeID{
		{},
		workflow.NewTypeID(reflect.TypeFor[string]()),
		workflow.NewTypeID(reflect.TypeFor[*workflow.Executor]()),
		workflow.NewTypeID(reflect.TypeFor[workflow.RequestPort]()),
		workflow.NewTypeID(reflect.TypeFor[map[string]int]()),
		workflow.NewTypeID(reflect.TypeFor[func(int) (string, error)]()),
		workflow.NewTypeID(reflect.TypeFor[typeIDPointerOnlyMarker]()),
	}

	for _, id := range cases {
		t.Run(id.String(), func(t *testing.T) {
			data, err := json.Marshal(id)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got workflow.TypeID
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got != id {
				t.Fatalf("roundtrip = %+v, want %+v", got, id)
			}
		})
	}
}

func TestTypeIDManualJSONCanMatchRuntimeType(t *testing.T) {
	var id workflow.TypeID
	if err := json.Unmarshal([]byte(`{"PackageName":"","TypeName":"int"}`), &id); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !id.Match(reflect.TypeFor[int]()) {
		t.Fatal("manual int TypeID should match int")
	}
	if id.Match(reflect.TypeFor[string]()) {
		t.Fatal("manual int TypeID should not match string")
	}
}

func TestTypeIDMatchUsesCanonicalIdentity(t *testing.T) {
	namedIntID := workflow.NewTypeID(reflect.TypeFor[typeIDNamedInt]())
	if !namedIntID.Match(reflect.TypeFor[*typeIDNamedInt]()) {
		t.Fatal("named int TypeID should match pointer to the same named type")
	}
	if namedIntID.Match(reflect.TypeFor[int]()) {
		t.Fatal("named int TypeID should not match builtin int")
	}
	if namedIntID.Match(reflect.TypeFor[typeIDOtherNamedInt]()) {
		t.Fatal("named int TypeID should not match a different named int type")
	}

	payloadID := workflow.NewTypeID(reflect.TypeFor[typeIDPayload]())
	if payloadID.Match(reflect.TypeFor[typeIDOtherPayload]()) {
		t.Fatal("payload TypeID should not match a different named struct with the same fields")
	}
}

func TestTypeIDMatchPolymorphicWithCachedInterfaces(t *testing.T) {
	pointerOnlyID := workflow.NewTypeID(reflect.TypeFor[typeIDPointerOnlyMarker]())
	if pointerOnlyID.Match(reflect.TypeFor[*typeIDPointerOnlyValue]()) {
		t.Fatal("interface TypeID should not exactly match implementing pointer type")
	}
	if !pointerOnlyID.MatchPolymorphic(reflect.TypeFor[*typeIDPointerOnlyValue]()) {
		t.Fatal("interface TypeID should polymorphically match implementing pointer type")
	}
	if pointerOnlyID.MatchPolymorphic(reflect.TypeFor[typeIDPointerOnlyValue]()) {
		t.Fatal("pointer-receiver implementation should not match by value")
	}
	if pointerOnlyID.MatchPolymorphic(reflect.TypeFor[string]()) {
		t.Fatal("interface TypeID should not polymorphically match unrelated string")
	}

	valueID := workflow.NewTypeID(reflect.TypeFor[typeIDValueMarker]())
	if !valueID.MatchPolymorphic(reflect.TypeFor[typeIDValueMarkerValue]()) {
		t.Fatal("interface TypeID should polymorphically match value implementation")
	}
	if !valueID.MatchPolymorphic(reflect.TypeFor[*typeIDValueMarkerValue]()) {
		t.Fatal("interface TypeID should polymorphically match pointer to value implementation")
	}
}

func TestTypeIDMatchPolymorphicWithStandardInterfaces(t *testing.T) {
	anyID := workflow.NewTypeID(reflect.TypeFor[any]())
	if !anyID.MatchPolymorphic(reflect.TypeFor[string]()) {
		t.Fatal("any TypeID should polymorphically match string")
	}
	if !anyID.MatchPolymorphic(reflect.TypeFor[*typeIDPayload]()) {
		t.Fatal("any TypeID should polymorphically match pointer payload")
	}
	if anyID.MatchPolymorphic(nil) {
		t.Fatal("any TypeID should not polymorphically match nil")
	}

	errorID := workflow.NewTypeID(reflect.TypeFor[error]())
	if !errorID.MatchPolymorphic(reflect.TypeFor[*typeIDCustomError]()) {
		t.Fatal("error TypeID should polymorphically match custom error pointer")
	}
	if errorID.MatchPolymorphic(reflect.TypeFor[typeIDCustomError]()) {
		t.Fatal("error TypeID should not match custom error value without Error method")
	}
}

func TestTypeIDMatchPolymorphicFromPointerToInterface(t *testing.T) {
	interfaceID := workflow.NewTypeID(reflect.TypeFor[*typeIDPointerOnlyMarker]())
	if interfaceID != workflow.NewTypeID(reflect.TypeFor[typeIDPointerOnlyMarker]()) {
		t.Fatal("pointer to interface should share the interface TypeID")
	}
	if !interfaceID.MatchPolymorphic(reflect.TypeFor[*typeIDPointerOnlyValue]()) {
		t.Fatal("pointer-to-interface TypeID should cache the interface runtime type")
	}
}

func TestTypeIDRequestPortInfoCachesRuntimeTypes(t *testing.T) {
	port := workflow.RequestPort{
		ID:       "ContentPort",
		Request:  reflect.TypeFor[*message.FunctionCallContent](),
		Response: reflect.TypeFor[message.Content](),
	}
	info := workflow.NewRequestPortInfo(port)

	if !info.RequestType.Match(reflect.TypeFor[*message.FunctionCallContent]()) {
		t.Fatal("request TypeID should match pointer request type")
	}
	if !info.ResponseType.MatchPolymorphic(reflect.TypeFor[*message.FunctionResultContent]()) {
		t.Fatal("response TypeID should polymorphically match concrete content")
	}
}

func expectedTypeID(typ reflect.Type) workflow.TypeID {
	typ = dereferenceType(typ)
	if typ == nil {
		return workflow.TypeID{}
	}
	typeName := typ.Name()
	if typeName == "" {
		typeName = typ.String()
	}
	return workflow.TypeID{PackageName: typ.PkgPath(), TypeName: typeName}
}

func dereferenceType(typ reflect.Type) reflect.Type {
	seen := make(map[reflect.Type]struct{})
	for typ != nil && typ.Kind() == reflect.Pointer {
		if _, ok := seen[typ]; ok {
			return nil
		}
		seen[typ] = struct{}{}
		typ = typ.Elem()
	}
	return typ
}
