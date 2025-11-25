// Copyright (c) Microsoft. All rights reserved.

package functool_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework/go/tool/functool"
)

// Test FuncTool
func TestFuncTool_Basic(t *testing.T) {
	type Input struct {
		Message string `json:"message"`
	}

	handler := func(ctx context.Context, input Input) (string, error) {
		return "output: " + input.Message, nil
	}

	funcDef := &functool.Func{
		Name:        "test_func",
		Description: "Test function",
	}

	tl, err := functool.New(funcDef, handler)
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

	funcDef := &functool.Func{
		Name:        "must_func",
		Description: "Must function",
	}

	tl := functool.MustNew(funcDef, handler)
	if tl == nil {
		t.Fatal("expected tool, got nil")
	}

	name, _ := tl.Name(), tl.Description()
	if name != "must_func" {
		t.Errorf("expected name 'must_func', got %q", name)
	}
}

func TestFuncTool_CallMissingArg0(t *testing.T) {
	tl := functool.MustNew(
		&functool.Func{
			Name: "test",
		},
		func(ctx context.Context, input string) (string, error) {
			return "result", nil
		},
	)

	// Call without required arg0
	_, err := tl.Call(t.Context(), json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing arg0, got nil")
	}
}

func TestFuncTool_CallStruct(t *testing.T) {
	type In struct {
		V string `json:"v"`
	}
	tl := functool.MustNew(
		&functool.Func{
			Name: "test",
		},
		func(ctx context.Context, input In) (string, error) {
			return input.V, nil
		},
	)

	ret, err := tl.Call(t.Context(), json.RawMessage(`{"v":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ret.(string) != "hello" {
		t.Errorf("expected 'hello', got %q", ret.(string))
	}
}

func TestFuncTool_CallString(t *testing.T) {
	tl := functool.MustNew(
		&functool.Func{
			Name: "test",
		},
		func(ctx context.Context, input string) (string, error) {
			return input, nil
		},
	)

	ret, err := tl.Call(t.Context(), json.RawMessage(`{"arg0":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ret.(string) != "hello" {
		t.Errorf("expected 'hello', got %q", ret.(string))
	}
}

func TestFuncTool_CallNoArg(t *testing.T) {
	tl := functool.MustNew(
		&functool.Func{
			Name: "test",
		},
		func(ctx context.Context, _ struct{}) (string, error) {
			return "hello", nil
		},
	)

	ret, err := tl.Call(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ret.(string) != "hello" {
		t.Errorf("expected 'hello', got %q", ret.(string))
	}
}

func TestFuncTool_CallNoRet(t *testing.T) {
	tl := functool.MustNew(
		&functool.Func{
			Name: "test",
		},
		func(ctx context.Context, _ struct{}) (struct{}, error) {
			return struct{}{}, nil
		},
	)

	ret, err := tl.Call(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ret != struct{}{} {
		t.Errorf("expected empty struct{}, got %v", ret)
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

	tl := functool.MustNew(
		&functool.Func{
			Name: "error_func",
		},
		handler,
	)

	_, err := tl.Call(t.Context(), json.RawMessage(`{"value":"test"}`))
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}
