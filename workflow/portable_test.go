// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/workflow"
)

func TestPortableValueRoundtrip(t *testing.T) {
	testRountrip(t, "string")
	testRountrip(t, 42)
	testRountrip(t, true)
	testRountrip(t, 3.14)
	testRountrip(t, message.NewText("hello"))
	testRountrip(t, message.RoleAssistant)
	testRountrip(t, message.ErrorContent{Message: "error message"})
}

func testRountrip[T any](t *testing.T, v T) {
	t.Helper()
	testNonDelayedRountrip(t, v)
	testDelayedRoundtrip(t, v)
}

func testNonDelayedRountrip[T any](t *testing.T, v T) {
	t.Helper()
	pv := workflow.AnyPortableValue(v)
	if got, want := pv.TypeID, portableValueTypeID(v); got != want {
		t.Errorf("nondelayed: TypeID = %+v, want %+v", got, want)
	}
	if _, ok := workflow.PortableValueAs[func()](pv); ok {
		t.Errorf("nondelayed: expected not to be func()")
	}
	got, ok := workflow.PortableValueAs[T](pv)
	if !ok {
		t.Errorf("nondelayed: expected to be able to convert to any")
	}
	if !reflect.DeepEqual(v, got) {
		t.Errorf("nondelayed: expected value %v, got %v", v, got)
	}
}

func testDelayedRoundtrip[T any](t *testing.T, v T) {
	t.Helper()
	pv := workflow.AnyPortableValue(v)
	data, err := json.Marshal(pv)
	if err != nil {
		t.Error(err)
	}
	var delayed workflow.PortableValue
	if err := json.Unmarshal(data, &delayed); err != nil {
		t.Error(err)
	}
	if got, want := delayed.TypeID, portableValueTypeID(v); got != want {
		t.Errorf("delayed: TypeID = %+v, want %+v", got, want)
	}
	if !delayed.Delayed() {
		t.Errorf("delayed: expected delayed value after JSON unmarshal")
	}
	if _, ok := workflow.PortableValueAs[func()](delayed); ok {
		t.Errorf("delayed: expected not to be func()")
	}
	got, ok := workflow.PortableValueAs[T](delayed)
	if !ok {
		t.Errorf("delayed: expected to be able to convert to any")
	}
	if !reflect.DeepEqual(v, got) {
		t.Errorf("delayed: expected value %v, got %v", v, got)
	}
	if !delayed.Delayed() {
		t.Errorf("delayed: by-value conversion should not update the original value cache")
	}
	gotAny, ok := delayed.As(reflect.TypeOf(v))
	if !ok {
		t.Errorf("delayed: expected pointer-receiver conversion to succeed")
	}
	if !reflect.DeepEqual(v, gotAny) {
		t.Errorf("delayed: expected pointer-receiver value %v, got %v", v, gotAny)
	}
	if delayed.Delayed() {
		t.Errorf("delayed: expected delayed value to use deserialized cache after conversion")
	}
}

func TestAnyPortableValue_ReturnsPortableValueUnmodified(t *testing.T) {
	pv := workflow.AnyPortableValue(0)
	got := workflow.AnyPortableValue(pv)
	if got.TypeID != pv.TypeID {
		t.Fatalf("TypeID = %+v, want %+v", got.TypeID, pv.TypeID)
	}
	value, ok := workflow.PortableValueAs[int](got)
	if !ok {
		t.Fatal("expected wrapped PortableValue to retain original int value")
	}
	if value != 0 {
		t.Fatalf("value = %d, want 0", value)
	}
}

func TestAnyPortableValue_DereferencesPortableValuePointer(t *testing.T) {
	pv := workflow.AnyPortableValue(0)
	got := workflow.AnyPortableValue(&pv)
	if got.TypeID != pv.TypeID {
		t.Fatalf("TypeID = %+v, want %+v", got.TypeID, pv.TypeID)
	}
	value, ok := workflow.PortableValueAs[int](got)
	if !ok {
		t.Fatal("expected wrapped *PortableValue to retain original int value")
	}
	if value != 0 {
		t.Fatalf("value = %d, want 0", value)
	}
}

func TestPortableValueAny_DecodesDelayedPrimitive(t *testing.T) {
	pv := workflow.AnyPortableValue("hello")
	delayed := delayedPortableValue(t, pv)
	if got := delayed.Any(); got != "hello" {
		t.Fatalf("Any() = %v (%T), want hello (string)", got, got)
	}
}

func TestPortableValueAny_DecodesDelayedRuntimeType(t *testing.T) {
	delayed := delayedPortableValue(t, workflow.AnyPortableValue(message.RoleAssistant))
	got := delayed.Any()
	role, ok := got.(message.Role)
	if !ok {
		t.Fatalf("Any() = %v (%T), want message.Role", got, got)
	}
	if role != message.RoleAssistant {
		t.Fatalf("Any() = %q, want %q", role, message.RoleAssistant)
	}
}

func TestPortableValueAny_DecodesDelayedPointerRuntimeType(t *testing.T) {
	delayed := delayedPortableValue(t, workflow.AnyPortableValue(&message.FunctionCallContent{
		CallID:    "call-1",
		Name:      "lookup",
		Arguments: `{"city":"Seattle"}`,
	}))
	got := delayed.Any()
	call, ok := got.(*message.FunctionCallContent)
	if !ok {
		t.Fatalf("Any() = %v (%T), want *message.FunctionCallContent", got, got)
	}
	if call.CallID != "call-1" || call.Name != "lookup" || call.Arguments != `{"city":"Seattle"}` {
		t.Fatalf("Any() = %+v, want decoded function call", call)
	}
}

func delayedPortableValue(t *testing.T, pv workflow.PortableValue) workflow.PortableValue {
	t.Helper()
	data, err := json.Marshal(pv)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var delayed workflow.PortableValue
	if err := json.Unmarshal(data, &delayed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return delayed
}

func TestPortableValueMarshal_PreservesUntouchedDelayedRawJSON(t *testing.T) {
	// A delayed value with an unregistered TypeID and a large integer that
	// cannot be represented exactly by float64. Re-marshaling without any typed
	// access must re-emit the original bytes verbatim, so a durable-JSON
	// restore -> re-checkpoint cycle stays idempotent and loses no fidelity.
	const bigInt = "9007219254740993" // > 2^53
	wire := []byte(`{"TypeID":{"PackageName":"example.invalid/missing","TypeName":"Missing"},"Value":` + bigInt + `}`)

	var delayed workflow.PortableValue
	if err := json.Unmarshal(wire, &delayed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !delayed.Delayed() {
		t.Fatalf("expected delayed value after unmarshal")
	}

	// Re-marshal WITHOUT any typed access.
	data, err := json.Marshal(delayed)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var wrapper struct {
		Value json.RawMessage
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("Unmarshal wrapper: %v", err)
	}
	if got := string(wrapper.Value); got != bigInt {
		t.Fatalf("re-emitted Value = %s, want %s (large-integer fidelity lost)", got, bigInt)
	}
}

func TestPortableValue_RejectsNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil PortableValue")
		}
	}()
	_ = workflow.AnyPortableValue(nil)
}

func TestPortableValue_RejectsZeroJSON(t *testing.T) {
	if _, err := json.Marshal(workflow.PortableValue{}); err == nil {
		t.Fatal("expected marshal error for zero PortableValue")
	}
	var got workflow.PortableValue
	if err := json.Unmarshal([]byte("null"), &got); err == nil {
		t.Fatal("expected unmarshal error for null PortableValue")
	}
}

func portableValueTypeID(v any) workflow.TypeID {
	if pv, ok := v.(workflow.PortableValue); ok {
		return pv.TypeID
	}
	return workflow.NewTypeID(reflect.TypeOf(v))
}
