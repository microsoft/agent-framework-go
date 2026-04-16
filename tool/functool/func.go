// Copyright (c) Microsoft. All rights reserved.

package functool

import (
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/microsoft/agent-framework-go/tool"
)

// Func represents a tool that wraps a Go function to make it callable by AI models.
type Func struct {
	Name        string
	Description string

	inputFormat  *jsonformat.Format
	inputWrapped bool
	outputFormat *jsonformat.Format
}

// A Handler handles a call to tools/call.
//
// This is a low-level API. It does not do any pre- or post-processing of the
// request or result: the params contain raw arguments, no input validation
// is performed, and the result is returned to the user as-is, without any
// validation of the output.
type Handler func(ctx tool.Context, args string) (any, error)

// A HandlerFor handles a call to tools/call with typed arguments and results.
//
// [HandlerFor] provides significant functionality out of the box, and enforces
// that the tool conforms to the Agent spec:
//   - The In type provides a default input schema for the tool.
//   - The input value is automatically unmarshaled from req.Params.Arguments.
//   - The input value is automatically validated against its input schema.
//     Invalid input is rejected before getting to the handler.
type HandlerFor[In, Out any] func(tool.Context, In) (Out, error)

var _ tool.Tool = (*Tool)(nil)
var _ tool.FuncTool = (*Tool)(nil)

type Tool struct {
	Func    Func
	Handler Handler
}

func (t *Tool) Name() string {
	return t.Func.Name
}

func (t *Tool) Description() string {
	return t.Func.Description
}

func (t *Tool) Schema() any {
	if t.Func.inputFormat == nil {
		return nil
	}
	return t.Func.inputFormat.Schema()
}

func (t *Tool) ReturnSchema() any {
	if t.Func.outputFormat == nil {
		return nil
	}
	return t.Func.outputFormat.Schema()
}

func (t *Tool) Call(ctx tool.Context, args string) (any, error) {
	if args == "" {
		args = "{}"
	}
	return t.Handler(ctx, args)
}

type inputWrapper[T any] struct {
	Arg0 T `json:"arg0"`
}

func inputFormatFor[T any]() (format *jsonformat.Format, wrapped bool, err error) {
	typ := reflect.TypeFor[T]()
	if typ == reflect.TypeFor[any]() {
		return nil, false, fmt.Errorf("input type any is not supported by HandlerFor; use Handler for dynamic inputs")
	}
	if typ.Kind() != reflect.Struct {
		typ = reflect.TypeFor[inputWrapper[T]]()
		wrapped = true
	}
	format, err = jsonformat.ForType(typ)
	return format, wrapped, err
}

func outputFormatFor[T any]() (*jsonformat.Format, error) {
	typ := reflect.TypeFor[T]()
	if typ == reflect.TypeFor[any]() {
		return jsonformat.Any(), nil
	}
	return jsonformat.ForType(typ)
}

func MustNew[In, Out any](fnp *Func, h HandlerFor[In, Out]) *Tool {
	t, err := New(fnp, h)
	if err != nil {
		panic(err)
	}
	return t
}

func New[In, Out any](fnp *Func, h HandlerFor[In, Out]) (*Tool, error) {
	t := Tool{
		Func: *fnp,
	}
	var err error
	t.Func.inputFormat, t.Func.inputWrapped, err = inputFormatFor[In]()
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	t.Func.outputFormat, err = outputFormatFor[Out]()
	if err != nil {
		return nil, fmt.Errorf("output schema: %w", err)
	}
	resolved, err := t.Func.outputFormat.ResolvedSchema()
	if err != nil {
		return nil, fmt.Errorf("resolving output schema: %w", err)
	}

	t.Handler = func(ctx tool.Context, args string) (any, error) {
		var in In
		if t.Func.inputWrapped {
			// Extract wrapped value.
			var decodedArgs inputWrapper[In]
			if err := jsonformat.Unmarshal(t.Func.inputFormat, []byte(args), &decodedArgs); err != nil {
				return nil, err
			}
			in = decodedArgs.Arg0
		} else {
			if err := jsonformat.Unmarshal(t.Func.inputFormat, []byte(args), &in); err != nil {
				return nil, err
			}
		}
		// Call typed handler.
		out, err := h(ctx, in)
		if err != nil {
			return nil, err
		}
		// Validating against "struct{}" doesn't work (nor make sense), see https://github.com/google/jsonschema-go/issues/23.
		// Also, validation of structs directly doesn't work - we need to marshal to JSON first.
		if reflect.TypeFor[Out]() != reflect.TypeFor[struct{}]() {
			// ApplyDefaults and Validate work on the unmarshaled value
			if err := resolved.ApplyDefaults(&out); err != nil {
				return nil, fmt.Errorf("applying output schema defaults: %w", err)
			}
			// Marshal to JSON and unmarshal to map for validation (structs can't be validated directly)
			// Skip validation for types that contain structs as jsonschema-go can't validate them directly
			// The schema is still used for documentation purposes
			if !containsStruct(reflect.TypeOf(out)) {
				if err := resolved.Validate(&out); err != nil {
					return nil, fmt.Errorf("validating output: %w", err)
				}
			}
		}
		return out, nil
	} // end of handler

	return &t, nil
}

// containsStruct checks if a type is or contains a struct that would fail jsonschema validation.
// This includes structs, pointers to structs, slices/arrays of structs, and maps with struct values.
func containsStruct(t reflect.Type) bool {
	if t == nil {
		return false
	}

	switch t.Kind() {
	case reflect.Struct:
		return true
	case reflect.Pointer:
		return containsStruct(t.Elem())
	case reflect.Slice, reflect.Array:
		return containsStruct(t.Elem())
	case reflect.Map:
		return containsStruct(t.Elem())
	default:
		return false
	}
}
