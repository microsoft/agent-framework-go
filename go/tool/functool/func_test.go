// Copyright (c) Microsoft. All rights reserved.

package functool_test

import (
	"context"
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

	name, desc := tl.ToolInfo()
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

	name, _ := tl.ToolInfo()
	if name != "must_func" {
		t.Errorf("expected name 'must_func', got %q", name)
	}
}

func TestFuncTool_MissingArg0(t *testing.T) {
	type Input struct {
		Value string `json:"value"`
	}

	handler := func(ctx context.Context, input Input) (string, error) {
		return "result", nil
	}

	tl := functool.MustNew(
		&functool.Func{
			Name: "test",
		},
		handler,
	)

	// Call without required arg0
	_, err := tl.Call(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing arg0, got nil")
	}
}

func TestFuncTool_HandlerError(t *testing.T) {
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

	_, err := tl.Call(context.Background(), map[string]any{
		"arg0": map[string]any{"value": "test"},
	})
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}
