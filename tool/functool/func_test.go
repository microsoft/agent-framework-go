// Copyright (c) Microsoft. All rights reserved.

package functool_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/tool"
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

func TestFuncTool_NewRejectsNilHandler(t *testing.T) {
	var handler functool.HandlerFor[struct{}, string]
	tool, err := functool.New(functool.Config{Name: "nil-handler"}, handler)
	if err == nil || err.Error() != "functool: handler is required" {
		t.Fatalf("New() error = %v, want handler required error", err)
	}
	if tool != nil {
		t.Fatalf("New() tool = %T, want nil", tool)
	}
}

func TestFuncTool_CallMissingValue(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}

	tl := functool.MustNew(cfg, func(ctx context.Context, input string) (string, error) {
		t.Fatal("handler called without the required value argument")
		return "result", nil
	})

	_, err := tl.Call(t.Context(), `{}`)
	if err == nil {
		t.Error("expected error for missing value, got nil")
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

func TestFuncTool_PointerOutputNilReturnsZeroValue(t *testing.T) {
	type Out struct {
		Msg string `json:"msg"`
	}
	type In struct {
		A int `json:"a"`
	}
	tl := functool.MustNew(functool.Config{Name: "test"},
		func(ctx context.Context, in In) (*Out, error) {
			return nil, nil // legitimate "no result"
		})

	ret, err := tl.Call(t.Context(), `{"a":1}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	got, ok := ret.(*Out)
	if !ok {
		t.Fatalf("result type = %T, want *Out", ret)
	}
	if got == nil {
		t.Fatal("result is nil, want zero-value *Out")
	}
	if got.Msg != "" {
		t.Errorf("Msg = %q, want empty", got.Msg)
	}
}

// namedPtr is a named pointer type; reflect.New produces an unnamed *outStruct
// that is not directly assertable to it.
type outStruct struct {
	Msg string `json:"msg"`
}

type namedPtr *outStruct

func TestFuncTool_NamedPointerOutputNilReturnsZeroValue(t *testing.T) {
	type In struct {
		A int `json:"a"`
	}
	tl := functool.MustNew(functool.Config{Name: "test"},
		func(ctx context.Context, in In) (namedPtr, error) {
			return nil, nil
		})

	ret, err := tl.Call(t.Context(), `{"a":1}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	got, ok := ret.(namedPtr)
	if !ok {
		t.Fatalf("result type = %T, want namedPtr", ret)
	}
	if got == nil {
		t.Fatal("result is nil, want zero-value namedPtr")
	}
	if got.Msg != "" {
		t.Errorf("Msg = %q, want empty", got.Msg)
	}
}

func TestFuncTool_CallString(t *testing.T) {
	cfg := functool.Config{
		Name: "test",
	}
	tl := functool.MustNew(cfg, func(ctx context.Context, input string) (string, error) {
		return input, nil
	})

	ret, err := tl.Call(t.Context(), `{"value":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if ret.(string) != "hello" {
		t.Errorf("expected 'hello', got %q", ret.(string))
	}
}

func TestFuncTool_WrappedInput_UsesTypeBasedArgument(t *testing.T) {
	n := 42
	numbers := []int{1, 2}
	entries := map[string]int{"count": 2, "value": 3, "values": 4, "entries": 5}
	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cfg := functool.Config{Name: "test"}
	for _, tc := range []struct {
		name      string
		tool      tool.FuncTool
		argument  string
		jsonValue string
		want      any
	}{
		{
			name: "string",
			tool: functool.MustNew(cfg, func(_ context.Context, input string) (string, error) {
				return input, nil
			}),
			argument:  "value",
			jsonValue: `"hello"`,
			want:      "hello",
		},
		{
			name: "boolean",
			tool: functool.MustNew(cfg, func(_ context.Context, input bool) (bool, error) {
				return input, nil
			}),
			argument:  "value",
			jsonValue: `true`,
			want:      true,
		},
		{
			name: "integer",
			tool: functool.MustNew(cfg, func(_ context.Context, input int) (int, error) {
				return input, nil
			}),
			argument:  "value",
			jsonValue: `42`,
			want:      42,
		},
		{
			name: "slice",
			tool: functool.MustNew(cfg, func(_ context.Context, input []int) ([]int, error) {
				return input, nil
			}),
			argument:  "values",
			jsonValue: `[1,2]`,
			want:      []int{1, 2},
		},
		{
			name: "pointer to slice",
			tool: functool.MustNew(cfg, func(_ context.Context, input *[]int) (*[]int, error) {
				return input, nil
			}),
			argument:  "values",
			jsonValue: `[1,2]`,
			want:      &numbers,
		},
		{
			name: "bytes",
			tool: functool.MustNew(cfg, func(_ context.Context, input []byte) (int, error) {
				return len(input), nil
			}),
			argument:  "values",
			jsonValue: `[1,2]`,
			want:      2,
		},
		{
			name: "array",
			tool: functool.MustNew(cfg, func(_ context.Context, input [2]int) ([2]int, error) {
				return input, nil
			}),
			argument:  "values",
			jsonValue: `[1,2]`,
			want:      [2]int{1, 2},
		},
		{
			name: "nil slice",
			tool: functool.MustNew(cfg, func(_ context.Context, input []int) ([]int, error) {
				return input, nil
			}),
			argument:  "values",
			jsonValue: `null`,
			want:      []int(nil),
		},
		{
			name: "map",
			tool: functool.MustNew(cfg, func(_ context.Context, input map[string]int) (map[string]int, error) {
				return input, nil
			}),
			argument:  "entries",
			jsonValue: `{"count":2,"value":3,"values":4,"entries":5}`,
			want:      entries,
		},
		{
			name: "pointer to map",
			tool: functool.MustNew(cfg, func(_ context.Context, input *map[string]int) (*map[string]int, error) {
				return input, nil
			}),
			argument:  "entries",
			jsonValue: `{"count":2,"value":3,"values":4,"entries":5}`,
			want:      &entries,
		},
		{
			name: "pointer",
			tool: functool.MustNew(cfg, func(_ context.Context, input *int) (*int, error) {
				return input, nil
			}),
			argument:  "value",
			jsonValue: `42`,
			want:      &n,
		},
		{
			name: "nil pointer",
			tool: functool.MustNew(cfg, func(_ context.Context, input *int) (bool, error) {
				return input == nil, nil
			}),
			argument:  "value",
			jsonValue: `null`,
			want:      true,
		},
		{
			name: "time",
			tool: functool.MustNew(cfg, func(_ context.Context, input time.Time) (time.Time, error) {
				return input, nil
			}),
			argument:  "value",
			jsonValue: `"2026-01-02T03:04:05Z"`,
			want:      timestamp,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, ok := tc.tool.Schema().(*jsonschema.Schema)
			if !ok {
				t.Fatalf("schema = %T, want *jsonschema.Schema", tc.tool.Schema())
			}
			if schema.Type != "object" || len(schema.Properties) != 1 || schema.Properties[tc.argument] == nil {
				t.Fatalf("schema type = %q, properties = %v; want an object with only %s", schema.Type, schema.Properties, tc.argument)
			}
			if !slices.Equal(schema.Required, []string{tc.argument}) {
				t.Errorf("required = %v, want [%s]", schema.Required, tc.argument)
			}
			wrapped := fmt.Sprintf(`{%q:%s}`, tc.argument, tc.jsonValue)
			for _, args := range []string{
				wrapped,
				" \t\n" + wrapped + "\r\n",
			} {
				got, err := tc.tool.Call(t.Context(), args)
				if err != nil {
					t.Errorf("Call(%q): %v", args, err)
					continue
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("Call(%q) = %#v, want %#v", args, got, tc.want)
				}
			}
			for _, args := range []string{
				`{}`,
				tc.jsonValue,
				fmt.Sprintf(`{%q:%s,"extra":true}`, tc.argument, tc.jsonValue),
			} {
				if _, err := tc.tool.Call(t.Context(), args); err == nil {
					t.Errorf("Call(%s) succeeded, want a validation error", args)
				}
			}
			for _, name := range []string{"Arg0", "value", "values", "entries"} {
				if name == tc.argument {
					continue
				}
				args := fmt.Sprintf(`{%q:%s}`, name, tc.jsonValue)
				if _, err := tc.tool.Call(t.Context(), args); err == nil {
					t.Errorf("Call(%s) succeeded, want a validation error", args)
				}
			}
		})
	}
}

func TestFuncTool_ArgName(t *testing.T) {
	n := 42
	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cfg := functool.Config{Name: "test", ArgName: "input"}
	calls := 0
	for _, tc := range []struct {
		name      string
		tool      tool.FuncTool
		jsonValue string
		want      any
	}{
		{
			name: "string",
			tool: functool.MustNew(cfg, func(_ context.Context, input string) (string, error) {
				calls++
				return input, nil
			}),
			jsonValue: `"hello"`,
			want:      "hello",
		},
		{
			name: "slice",
			tool: functool.MustNew(cfg, func(_ context.Context, input []int) ([]int, error) {
				calls++
				return input, nil
			}),
			jsonValue: `[1,2]`,
			want:      []int{1, 2},
		},
		{
			name: "array",
			tool: functool.MustNew(cfg, func(_ context.Context, input [2]int) ([2]int, error) {
				calls++
				return input, nil
			}),
			jsonValue: `[1,2]`,
			want:      [2]int{1, 2},
		},
		{
			name: "map",
			tool: functool.MustNew(cfg, func(_ context.Context, input map[string]int) (map[string]int, error) {
				calls++
				return input, nil
			}),
			jsonValue: `{"input":1,"values":2,"entries":3}`,
			want:      map[string]int{"input": 1, "values": 2, "entries": 3},
		},
		{
			name: "pointer",
			tool: functool.MustNew(cfg, func(_ context.Context, input *int) (*int, error) {
				calls++
				return input, nil
			}),
			jsonValue: `42`,
			want:      &n,
		},
		{
			name: "nil pointer to slice",
			tool: functool.MustNew(cfg, func(_ context.Context, input *[]int) (bool, error) {
				calls++
				return input == nil, nil
			}),
			jsonValue: `null`,
			want:      true,
		},
		{
			name: "time",
			tool: functool.MustNew(cfg, func(_ context.Context, input time.Time) (time.Time, error) {
				calls++
				return input, nil
			}),
			jsonValue: `"2026-01-02T03:04:05Z"`,
			want:      timestamp,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, ok := tc.tool.Schema().(*jsonschema.Schema)
			if !ok {
				t.Fatalf("schema = %T, want *jsonschema.Schema", tc.tool.Schema())
			}
			if schema.Type != "object" || len(schema.Properties) != 1 || schema.Properties[cfg.ArgName] == nil {
				t.Fatalf("schema type = %q, properties = %v; want an object with only %s", schema.Type, schema.Properties, cfg.ArgName)
			}
			if !slices.Equal(schema.Required, []string{cfg.ArgName}) {
				t.Errorf("required = %v, want [%s]", schema.Required, cfg.ArgName)
			}

			before := calls
			args := fmt.Sprintf(`{%q:%s}`, cfg.ArgName, tc.jsonValue)
			got, err := tc.tool.Call(t.Context(), args)
			if err != nil {
				t.Fatalf("Call(%s): %v", args, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Call(%s) = %#v, want %#v", args, got, tc.want)
			}
			if calls != before+1 {
				t.Errorf("handler called %d times, want 1", calls-before)
			}

			before = calls
			for _, args := range []string{
				`{}`,
				tc.jsonValue,
				`{"input":{"wrong":true}}`,
				fmt.Sprintf(`{"input":%s,"extra":true}`, tc.jsonValue),
			} {
				if _, err := tc.tool.Call(t.Context(), args); err == nil {
					t.Errorf("Call(%s) succeeded, want a validation error", args)
				}
			}
			for _, name := range []string{"Arg0", "value", "values", "entries", "Input"} {
				args := fmt.Sprintf(`{%q:%s}`, name, tc.jsonValue)
				if _, err := tc.tool.Call(t.Context(), args); err == nil {
					t.Errorf("Call(%s) succeeded, want a validation error", args)
				}
			}
			if calls != before {
				t.Errorf("invalid arguments reached the handler %d times", calls-before)
			}
		})
	}
}

func TestFuncTool_ArgName_LiteralAndIndependent(t *testing.T) {
	var tools []tool.FuncTool
	names := []string{"ids", "items,omitempty", `quoted"name`, "path/~", ""}
	for _, name := range names {
		tools = append(tools, functool.MustNew(functool.Config{Name: "test", ArgName: name},
			func(_ context.Context, input []int) ([]int, error) {
				return input, nil
			}))
	}
	for i, tl := range tools {
		name := names[i]
		if name == "" {
			name = "values"
		}
		t.Run(name, func(t *testing.T) {
			args := fmt.Sprintf(`{%q:[1,2]}`, name)
			got, err := tl.Call(t.Context(), args)
			if err != nil {
				t.Fatalf("Call(%s): %v", args, err)
			}
			if !reflect.DeepEqual(got, []int{1, 2}) {
				t.Errorf("Call(%s) = %#v, want [1 2]", args, got)
			}
		})
	}
}

func TestFuncTool_ArgName_RejectsObjectStruct(t *testing.T) {
	type input struct {
		Value string `json:"value"`
	}
	cfg := functool.Config{Name: "test", ArgName: "input"}
	for _, tc := range []struct {
		name string
		new  func() (tool.FuncTool, error)
	}{
		{
			name: "struct",
			new: func() (tool.FuncTool, error) {
				return functool.New(cfg, func(_ context.Context, in input) (string, error) {
					return in.Value, nil
				})
			},
		},
		{
			name: "pointer to struct",
			new: func() (tool.FuncTool, error) {
				return functool.New(cfg, func(_ context.Context, in *input) (string, error) {
					return in.Value, nil
				})
			},
		},
		{
			name: "no argument",
			new: func() (tool.FuncTool, error) {
				return functool.New(cfg, func(_ context.Context, _ struct{}) (string, error) {
					return "", nil
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tl, err := tc.new()
			if err == nil || err.Error() != "input schema: ArgName cannot be used with object-shaped struct inputs" {
				t.Fatalf("New() error = %v, want an ArgName configuration error", err)
			}
			if tl != nil {
				t.Fatalf("New() tool = %T, want nil", tl)
			}
		})
	}
}

func TestFuncTool_MapInput_ValidatesEntries(t *testing.T) {
	tl := functool.MustNew(functool.Config{Name: "test"}, func(_ context.Context, input map[string]int) (map[string]int, error) {
		t.Fatal("invalid arguments reached the handler")
		return input, nil
	})
	for _, args := range []string{
		`{"count":2}`,
		`{"entries":[1,2]}`,
		`{"entries":{"count":"two"}}`,
		`{"entries":{"count":2},"extra":true}`,
	} {
		if _, err := tl.Call(t.Context(), args); err == nil {
			t.Errorf("Call(%s) succeeded, want a validation error", args)
		}
	}
}

func TestFuncTool_WrappedInput_RejectsInvalidArguments(t *testing.T) {
	tl := functool.MustNew(functool.Config{Name: "test"}, func(_ context.Context, input []int) (int, error) {
		t.Fatal("invalid arguments reached the handler")
		return len(input), nil
	})
	for _, args := range []string{
		"",
		" \t\r\n",
		`[1,2]`,
		`{"items":[1,2]}`,
		`{"values":[1,2],"extra":true}`,
		`[1,"2"]`,
		`{"values":[1,"2"]}`,
		`1`,
		`{"values":1}`,
		`[1,2`,
		`[1,2] [3]`,
		`[1,2],"values":[3]`,
		"\u00a0[1,2]",
		`{"values":[1,2]`,
	} {
		if _, err := tl.Call(t.Context(), args); err == nil {
			t.Errorf("Call(%q) succeeded, want an error", args)
		}
	}
}

func TestFuncTool_WrappedInput_PropagatesHandlerError(t *testing.T) {
	expectedErr := errors.New("handler error")
	calls := 0
	tl := functool.MustNew(functool.Config{Name: "test"}, func(_ context.Context, input []int) (int, error) {
		calls++
		return 0, expectedErr
	})
	for _, args := range []string{`{"values":[1,2]}`, `{"values":[]}`} {
		if _, err := tl.Call(t.Context(), args); !errors.Is(err, expectedErr) {
			t.Errorf("Call(%s) error = %v, want %v", args, err, expectedErr)
		}
	}
	if calls != 2 {
		t.Errorf("handler called %d times, want 2", calls)
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
// flat arguments without a {"value":{...}} envelope.
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
