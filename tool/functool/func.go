// Copyright (c) Microsoft. All rights reserved.

package functool

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/microsoft/agent-framework-go/tool"
)

// Config represents the configuration for a FuncTool.
type Config struct {
	Name        string
	Description string

	// ArgName overrides the name of the single property wrapping the input.
	// When empty, [New] chooses the name based on the input type. Setting ArgName
	// for an object-shaped struct input causes [New] to return an error.
	ArgName string
}

// A Handler handles a call to tools/call.
//
// This is a low-level API. It does not do any pre- or post-processing of the
// request or result: the params contain raw arguments, no input validation
// is performed, and the result is returned to the user as-is, without any
// validation of the output.
type Handler func(ctx context.Context, args string) (any, error)

// A HandlerFor handles a call to tools/call with typed arguments and results.
//
// [HandlerFor] provides significant functionality out of the box, and enforces
// that the tool conforms to the Agent spec:
//   - The In type provides a default input schema for the tool.
//   - The input value is automatically unmarshaled from req.Params.Arguments.
//   - The input value is automatically validated against its input schema.
//     Invalid input is rejected before getting to the handler.
type HandlerFor[In, Out any] func(context.Context, In) (Out, error)

// MustNew is like [New] but panics if schema construction fails. It is intended
// for package-level initialization where a construction failure is a programming
// error that should surface immediately.
func MustNew[In, Out any](cfg Config, h HandlerFor[In, Out]) tool.FuncTool {
	t, err := New(cfg, h)
	if err != nil {
		panic(err)
	}
	return t
}

// New builds a [tool.FuncTool] from cfg and the typed handler h. It derives the
// JSON input schema from In and the output schema from Out, and validates
// arguments before invoking h. It returns an error if either schema cannot be
// constructed.
//
// The input schema always describes a JSON object. Object-shaped structs use
// their JSON fields as arguments. Other inputs require a single property:
//   - "values" for slices and arrays;
//   - "entries" for maps;
//   - "value" for all other types.
//
// Pointers follow their pointed-to type. For example, an []int input requires
// {"values":[1,2]}; a bare array is rejected. [Config.ArgName] overrides the
// wrapped property name and cannot be set for object-shaped struct inputs.
func New[In, Out any](cfg Config, h HandlerFor[In, Out]) (tool.FuncTool, error) {
	if h == nil {
		return nil, errors.New("functool: handler is required")
	}
	t := funcTool{
		cfg: cfg,
	}
	var err error
	t.inputFormat, t.inputKey, err = inputFormatFor[In](cfg.ArgName)
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	t.outputFormat, err = formatFor[Out]()
	if err != nil {
		return nil, fmt.Errorf("output schema: %w", err)
	}

	// When Out is a pointer type, a handler may legitimately return a typed-nil
	// pointer to mean "no result". A typed nil marshals to JSON null, which fails
	// validation against the (non-nullable, object-rooted) output schema derived
	// from the pointed-to type. Substitute the zero value of the element type in
	// that case, mirroring the MCP go-sdk's HandlerFor behavior.
	var elemZero Out
	outType := reflect.TypeFor[Out]()
	hasPointerOut := outType.Kind() == reflect.Pointer
	if hasPointerOut {
		// Convert to outType before asserting: for a named pointer type
		// (e.g. type P *T), reflect.New yields an unnamed *T that is not
		// directly assertable to P.
		elemZero = reflect.New(outType.Elem()).Convert(outType).Interface().(Out)
	}

	t.handler = func(ctx context.Context, args string) (any, error) {
		data := []byte(args)
		var in In
		if t.inputKey != "" {
			var decodedArgs map[string]In
			if err := t.inputFormat.Unmarshal(data, &decodedArgs); err != nil {
				return nil, err
			}
			in = decodedArgs[t.inputKey]
		} else {
			if err := t.inputFormat.Unmarshal(data, &in); err != nil {
				return nil, err
			}
		}
		// Call typed handler.
		out, err := h(ctx, in)
		if err != nil {
			return nil, err
		}
		if hasPointerOut && reflect.ValueOf(out).IsNil() {
			out = elemZero
		}
		if err := t.outputFormat.Normalize(&out); err != nil {
			return nil, fmt.Errorf("normalizing output: %w", err)
		}
		return out, nil
	}
	return &t, nil
}

type funcTool struct {
	cfg     Config
	handler Handler

	inputFormat  *jsonformat.Format
	inputKey     string
	outputFormat *jsonformat.Format
}

func (t *funcTool) Name() string {
	return t.cfg.Name
}

func (t *funcTool) Description() string {
	return t.cfg.Description
}

func (t *funcTool) Schema() any {
	if t.inputFormat == nil {
		return nil
	}
	return t.inputFormat.Schema
}

func (t *funcTool) ReturnSchema() any {
	if t.outputFormat == nil {
		return nil
	}
	return t.outputFormat.Schema
}

func (t *funcTool) Call(ctx context.Context, args string) (any, error) {
	return t.handler(ctx, args)
}

func inputFormatFor[T any](argName string) (*jsonformat.Format, string, error) {
	typ := reflect.TypeFor[T]()
	if typ == reflect.TypeFor[any]() {
		return nil, "", fmt.Errorf("input type any is not supported by HandlerFor; use Handler for dynamic inputs")
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Struct {
		format, err := formatFor[T]()
		if err != nil {
			return nil, "", err
		}
		if format.Schema.(*jsonschema.Schema).Type == "object" {
			if argName != "" {
				return nil, "", errors.New("ArgName cannot be used with object-shaped struct inputs")
			}
			return format, "", nil
		}
	}
	if argName == "" {
		switch typ.Kind() {
		case reflect.Slice, reflect.Array:
			argName = "values"
		case reflect.Map:
			argName = "entries"
		default:
			argName = "value"
		}
	}
	format, err := formatFor[struct {
		Value T `json:"value"`
	}]()
	if err != nil {
		return nil, "", err
	}
	schema := format.Schema.(*jsonschema.Schema)
	schema.Properties = map[string]*jsonschema.Schema{argName: schema.Properties["value"]}
	schema.Required = []string{argName}
	return format, argName, nil
}

func formatFor[T any]() (*jsonformat.Format, error) {
	typ := reflect.TypeFor[T]()
	responseFormat, err := jsonformat.ForType(typ)
	if err != nil {
		return nil, err
	}
	return jsonformat.FromResponseFormat(responseFormat)
}
