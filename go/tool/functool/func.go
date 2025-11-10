// Copyright (c) Microsoft. All rights reserved.

package functool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework/go/tool"
)

// A Handler handles a call to tools/call.
//
// This is a low-level API, for use with [Server.AddTool]. It does not do any
// pre- or post-processing of the request or result: the params contain raw
// arguments, no input validation is performed, and the result is returned to
// the user as-is, without any validation of the output.
type Handler func(_ context.Context, args string) (any, error)

// A HandlerFor handles a call to tools/call with typed arguments and results.
//
// [HandlerFor] provides significant functionality out of the box, and enforces
// that the tool conforms to the Agent spec:
//   - The In type provides a default input schema for the tool.
//   - The input value is automatically unmarshaled from req.Params.Arguments.
//   - The input value is automatically validated against its input schema.
//     Invalid input is rejected before getting to the handler.
//   - If the Out type is not the empty interface [any], it provides the
//     default output schema for the tool.
//   - An error result is treated as a tool error, rather than a protocol error.
type HandlerFor[In, Out any] func(context.Context, In) (Out, error)

var _ tool.Tool = (*Tool)(nil)
var _ tool.CallTool = (*Tool)(nil)

type Tool struct {
	Func    Func
	Handler Handler
}

func (t *Tool) ToolInfo() (name string, description string) {
	return t.Func.Name, t.Func.Description
}

func (t *Tool) Schema() any {
	if t.Func.InputSchema == nil {
		// This prevents the tool author from forgetting to write a schema where
		// one should be provided. If we papered over this by supplying the empty
		// schema, then every input would be validated and the problem wouldn't be
		// discovered until runtime, when the LLM sent bad data.
		panic("FuncTool.Schema: InputSchema is nil")
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"arg0": t.Func.InputSchema,
		},
		"required": []string{"arg0"},
	}
}

func (t *Tool) Call(ctx context.Context, args map[string]any) (any, error) {
	if _, ok := args["arg0"]; !ok {
		return nil, fmt.Errorf("missing required argument: arg0")
	}
	argsBytes, err := json.Marshal(args["arg0"])
	if err != nil {
		return nil, err
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

func New[In, Out any](fnp *Func, h HandlerFor[In, Out]) (*Tool, error) {
	t := Tool{
		Func: *fnp,
	}
	// Special handling for an "any" input: treat as an empty object.
	if reflect.TypeFor[In]() == reflect.TypeFor[any]() && t.Func.InputSchema == nil {
		t.Func.InputSchema = &jsonschema.Schema{Type: "object"}
	}
	var inputResolved *jsonschema.Resolved
	if _, err := setSchema[In](&t.Func.InputSchema, &inputResolved); err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}

	// Handling for zero values:
	//
	// If Out is a pointer type and we've derived the output schema from its
	// element type, use the zero value of its element type in place of a typed
	// nil.
	var (
		elemZero       any // only non-nil if Out is a pointer type
		outputResolved *jsonschema.Resolved
	)
	if t.Func.OutputSchema != nil || reflect.TypeFor[Out]() != reflect.TypeFor[any]() {
		var err error
		elemZero, err = setSchema[Out](&t.Func.OutputSchema, &outputResolved)
		if err != nil {
			return nil, fmt.Errorf("output schema: %v", err)
		}
	}

	t.Handler = func(ctx context.Context, args string) (any, error) {
		var input json.RawMessage
		if args != "" {
			input = json.RawMessage(args)
		}
		// Validate input and apply defaults.
		var err error
		input, err = applySchema(input, inputResolved)
		if err != nil {
			return nil, fmt.Errorf("validating \"arguments\": %v", err)
		}

		// Unmarshal and validate args.
		var in In
		if input != nil {
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}
		}

		// Call typed handler.
		out, err := h(ctx, in)
		// Handle server errors appropriately:
		// - If the handler returns a structured error (like jsonrpc2.WireError), return it directly
		// - If the handler returns a regular error, wrap it in a CallToolResult with IsError=true
		// - This allows tools to distinguish between protocol errors and tool execution errors
		if err != nil {
			return nil, err
		}

		// Marshal the output and put the RawMessage in the StructuredContent field.
		var outval any = out
		if elemZero != nil {
			// Avoid typed nil, which will serialize as JSON null.
			// Instead, use the zero value of the unpointered type.
			var z Out
			if any(out) == any(z) { // zero is only non-nil if Out is a pointer type
				outval = elemZero
			}
		}
		if outval != nil {
			outbytes, err := json.Marshal(outval)
			if err != nil {
				return nil, fmt.Errorf("marshaling output: %w", err)
			}
			outJSON := json.RawMessage(outbytes)
			// Validate the output JSON, and apply defaults.
			//
			// We validate against the JSON, rather than the output value, as
			// some types may have custom JSON marshalling (issue #447).
			outJSON, err = applySchema(outJSON, outputResolved)
			if err != nil {
				return nil, fmt.Errorf("validating tool output: %w", err)
			}
			return outJSON, nil
		}
		return nil, nil
	} // end of handler

	return &t, nil
}

// setSchema sets the schema and resolved schema corresponding to the type T.
//
// If sfield is nil, the schema is derived from T.
//
// Pointers are treated equivalently to non-pointers when deriving the schema.
// If an indirection occurred to derive the schema, a non-nil zero value is
// returned to be used in place of the typed nil zero value.
//
// Note that if sfield already holds a schema, zero will be nil even if T is a
// pointer: if the user provided the schema, they may have intentionally
// derived it from the pointer type, and handling of zero values is up to them.
func setSchema[T any](sfield *any, rfield **jsonschema.Resolved) (zero any, err error) {
	var internalSchema *jsonschema.Schema
	if *sfield == nil {
		rt := reflect.TypeFor[T]()
		if rt.Kind() == reflect.Pointer {
			rt = rt.Elem()
			zero = reflect.Zero(rt).Interface()
		}
		internalSchema, err = jsonschema.ForType(rt, &jsonschema.ForOptions{})
		if err == nil {
			*sfield = internalSchema
		}
	} else if err := remarshal(*sfield, &internalSchema); err != nil {
		return zero, err
	}
	if err != nil {
		return zero, err
	}
	*rfield, err = internalSchema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	return zero, err
}

// applySchema validates whether data is valid JSON according to the provided
// schema, after applying schema defaults.
//
// Returns the JSON value augmented with defaults.
func applySchema(data json.RawMessage, resolved *jsonschema.Resolved) (json.RawMessage, error) {
	// TODO: use reflection to create the struct type to unmarshal into.
	// Separate validation from assignment.

	// Use default JSON marshalling for validation.
	//
	// This avoids inconsistent representation due to custom marshallers, such as
	// time.Time (issue #449).
	//
	// Additionally, unmarshalling into a map ensures that the resulting JSON is
	// at least {}, even if data is empty. For example, arguments is technically
	// an optional property of callToolParams, and we still want to apply the
	// defaults in this case.
	//
	// TODO(rfindley): in which cases can resolved be nil?
	if resolved != nil {
		v := make(map[string]any)
		if len(data) > 0 {
			if err := json.Unmarshal(data, &v); err != nil {
				return nil, fmt.Errorf("unmarshaling arguments: %w", err)
			}
		}
		if err := resolved.ApplyDefaults(&v); err != nil {
			return nil, fmt.Errorf("applying schema defaults:\n%w", err)
		}
		if err := resolved.Validate(&v); err != nil {
			return nil, err
		}
		// We must re-marshal with the default values applied.
		var err error
		data, err = json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshalling with defaults: %v", err)
		}
	}
	return data, nil
}

// remarshal marshals from to JSON, and then unmarshals into to, which must be
// a pointer type.
func remarshal(from, to any) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, to); err != nil {
		return err
	}
	return nil
}

// Func represents a tool that wraps a Go function to make it callable by AI models.
type Func struct {
	Name                 string
	Description          string
	AdditionalProperties map[string]any
	InputSchema          any
	OutputSchema         any
}
