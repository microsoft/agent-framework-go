// Copyright (c) Microsoft. All rights reserved.

package functool_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

// Test FuncTool
func TestFuncTool_Basic(t *testing.T) {
	type Input struct {
		Message string `json:"message"`
	}

	handler := func(ctx context.Context, input Input) (string, error) {
		return "output: " + input.Message, nil
	}

	cfg := functool.Config{
		Name:        "test_func",
		Description: "Test function",
	}

	tl, err := functool.New(cfg, handler)
	if err != nil {
		t.Fatalf("expected no error creating FuncTool, got: %v", err)
	}

	name, desc := tl.Name(), tl.Description()
	if name != "test_func" {
		t.Errorf("expected name 'test_func', got %q", name)
	}
	if desc != "Test function" {
		t.Errorf("expected description 'Test function', got %q", desc)
	}

	// Test schema
	schema := tl.Schema()
	if schema == nil {
		t.Fatal("expected schema, got nil")
	}
}

func TestFuncTool_MustNew(t *testing.T) {
	handler := func(ctx context.Context, input string) (string, error) {
		return "result", nil
	}

	cfg := functool.Config{
		Name:        "must_func",
		Description: "Must function",
	}

	tl := functool.MustNew(cfg, handler)
	if tl == nil {
		t.Fatal("expected tool, got nil")
	}

	name, _ := tl.Name(), tl.Description()
	if name != "must_func" {
		t.Errorf("expected name 'must_func', got %q", name)
	}
}

func TestFuncTool_CallMissingArg0(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}

	tl := functool.MustNew(cfg, func(ctx context.Context, input string) (string, error) {
		return "result", nil
	})

	// Call without required arg0
	_, err := tl.Call(t.Context(), `{}`)
	if err == nil {
		t.Error("expected error for missing arg0, got nil")
	}
}

func TestFuncTool_CallStruct(t *testing.T) {
	type In struct {
		V string `json:"v"`
	}
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, input In) (string, error) {
		return input.V, nil
	})

	ret, err := tl.Call(t.Context(), `{"v":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if ret.(string) != "hello" {
		t.Errorf("expected 'hello', got %q", ret.(string))
	}
}

func TestFuncTool_CallString(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, input string) (string, error) {
		return input, nil
	})

	ret, err := tl.Call(t.Context(), `{"Arg0":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if ret.(string) != "hello" {
		t.Errorf("expected 'hello', got %q", ret.(string))
	}
}

func TestFuncTool_CallNoArg(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, _ struct{}) (string, error) {
		return "hello", nil
	})

	ret, err := tl.Call(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if ret.(string) != "hello" {
		t.Errorf("expected 'hello', got %q", ret.(string))
	}
}

func TestFuncTool_CallNoArgRejectsExtraFields(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, _ struct{}) (string, error) {
		return "hello", nil
	})

	_, err := tl.Call(t.Context(), `{"unexpected":"value"}`)
	if err == nil {
		t.Fatal("expected error for unexpected fields")
	}
}

func TestFuncTool_NoArgSchema(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, _ struct{}) (string, error) {
		return "hello", nil
	})

	schema, ok := tl.Schema().(*jsonschema.Schema)
	if !ok {
		t.Fatalf("expected jsonschema.Schema, got %T", tl.Schema())
	}
	if schema.Type != "object" {
		t.Fatalf("expected object schema, got %#v", schema)
	}
	if schema.AdditionalProperties == nil || schema.AdditionalProperties.Not == nil {
		t.Fatalf("expected additionalProperties=false semantics, got %#v", schema)
	}
}

func TestFuncTool_CallNoRet(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, nil
	})

	ret, err := tl.Call(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if ret != struct{}{} {
		t.Errorf("expected empty struct{}, got %v", ret)
	}
}

func TestFuncTool_CallAnyOutput(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, _ struct{}) (any, error) {
		return map[string]any{"status": "ok", "value": 42}, nil
	})

	ret, err := tl.Call(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	out, ok := ret.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", ret)
	}
	if !reflect.DeepEqual(out, map[string]any{"status": "ok", "value": 42}) {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestFuncTool_ReturnSchemaAnyOutput(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, _ struct{}) (any, error) {
		return "ok", nil
	})

	schema, ok := tl.ReturnSchema().(*jsonschema.Schema)
	if !ok {
		t.Fatalf("expected jsonschema.Schema, got %T", tl.ReturnSchema())
	}
	if !reflect.DeepEqual(schema, &jsonschema.Schema{}) {
		t.Fatalf("expected unconstrained schema for any output, got %#v", schema)
	}
}

func TestFuncTool_CallError(t *testing.T) {
	type Input struct {
		Value string `json:"value"`
	}

	expectedErr := errors.New("handler error")
	handler := func(ctx context.Context, input Input) (string, error) {
		return "", expectedErr
	}

	cfg := functool.Config{
		Name: "error_func",
	}
	tl := functool.MustNew(cfg, handler)

	_, err := tl.Call(t.Context(), `{"value":"test"}`)
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestFuncTool_NewRejectsAnyInput(t *testing.T) {
	_, err := functool.New(
		functool.Config{Name: "test"},
		func(ctx context.Context, input any) (string, error) {
			return "ok", nil
		},
	)
	if err == nil {
		t.Fatal("expected error for any input type")
	}
	if got := err.Error(); got != "input schema: input type any is not supported by HandlerFor; use Handler for dynamic inputs" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A *Struct input must produce the same flat schema as a Struct input and accept
// flat arguments, not be wrapped in an inputWrapper (which would require {"Arg0":{...}}).
func TestFuncTool_PointerStructInput_UsesFlatSchema(t *testing.T) {
	type In struct {
		V string `json:"v"`
	}
	tl := functool.MustNew(functool.Config{Name: "t", Description: "d"}, func(_ context.Context, in *In) (string, error) {
		if in == nil {
			return "", nil
		}
		return in.V, nil
	})
	ret, err := tl.Call(t.Context(), `{"v":"hi"}`)
	if err != nil {
		t.Fatalf("Call with flat args failed for *struct input: %v", err)
	}
	if ret != "hi" {
		t.Errorf("ret = %v, want hi", ret)
	}
}
