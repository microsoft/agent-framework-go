// Copyright (c) Microsoft. All rights reserved.

package functool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework/go/format/jsonformat"
	"github.com/microsoft/agent-framework/go/tool"
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
type Handler func(_ context.Context, args string) (any, error)

// A HandlerFor handles a call to tools/call with typed arguments and results.
//
// [HandlerFor] provides significant functionality out of the box, and enforces
// that the tool conforms to the Agent spec:
//   - The In type provides a default input schema for the tool.
//   - The input value is automatically unmarshaled from req.Params.Arguments.
//   - The input value is automatically validated against its input schema.
//     Invalid input is rejected before getting to the handler.
type HandlerFor[In, Out any] func(context.Context, In) (Out, error)

var _ tool.Tool = (*Tool)(nil)
var _ tool.FuncTool = (*Tool)(nil)

type Tool struct {
	Func    Func
	Handler Handler
}

func (t *Tool) ToolInfo() (name string, description string) {
	return t.Func.Name, t.Func.Description
}

func (t *Tool) Schema() any {
	return t.Func.inputFormat.Schema()
}

func (t *Tool) ReturnSchema() any {
	return t.Func.outputFormat.Schema()
}

func (t *Tool) Call(ctx context.Context, args map[string]any) (any, error) {
	var argsBytes []byte
	if args != nil {
		var err error
		argsBytes, err = json.Marshal(args)
		if err != nil {
			return nil, err
		}
	} else {
		argsBytes = []byte("{}")
	}
	return t.Handler(ctx, string(argsBytes))
}

func MustNew[In, Out any](fnp *Func, h HandlerFor[In, Out]) *Tool {
	t, err := New(fnp, h)
	if err != nil {
		panic(err)
	}
	return t
}

type inputWrapper[T any] struct {
	Arg0 T `json:"arg0"`
}

func formatFor[T any](needObject bool) (format *jsonformat.Format, wrapped bool, err error) {
	typ := reflect.TypeFor[T]()
	if typ == reflect.TypeFor[any]() {
		return jsonformat.Nothing(), false, nil
	}
	if needObject && typ.Kind() != reflect.Struct {
		typ = reflect.TypeFor[inputWrapper[T]]()
		wrapped = true
	}
	format, err = jsonformat.ForType(typ)
	return format, wrapped, err
}

func New[In, Out any](fnp *Func, h HandlerFor[In, Out]) (*Tool, error) {
	t := Tool{
		Func: *fnp,
	}
	var err error
	t.Func.inputFormat, t.Func.inputWrapped, err = formatFor[In](true)
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	t.Func.outputFormat, _, err = formatFor[Out](false)
	if err != nil {
		return nil, fmt.Errorf("output schema: %w", err)
	}
	outResolved, err := t.Func.outputFormat.ResolvedSchema()
	if err != nil {
		return nil, fmt.Errorf("resolving output schema: %w", err)
	}

	t.Handler = func(ctx context.Context, args string) (any, error) {
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
		if reflect.TypeFor[Out]() != reflect.TypeFor[struct{}]() {
			// ApplyDefaults and Validate work on the unmarshaled value
			if err := outResolved.ApplyDefaults(&out); err != nil {
				return nil, fmt.Errorf("applying output schema defaults: %w", err)
			}
			if err := outResolved.Validate(&out); err != nil {
				return nil, fmt.Errorf("validating output: %w", err)
			}
		}
		return out, nil
	} // end of handler

	return &t, nil
}
