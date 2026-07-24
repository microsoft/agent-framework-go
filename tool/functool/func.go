// Copyright (c) Microsoft. All rights reserved.

package functool

import (
	"context"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
	"github.com/microsoft/agent-framework-go/tool"
)

// Config represents the configuration for a FuncTool.
type Config struct {
	Name        string
	Description string
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

func MustNew[In, Out any](cfg Config, h HandlerFor[In, Out]) tool.FuncTool {
	t, err := New(cfg, h)
	if err != nil {
		panic(err)
	}
	return t
}

func New[In, Out any](cfg Config, h HandlerFor[In, Out]) (tool.FuncTool, error) {
	t := funcTool{
		cfg: cfg,
	}
	var err error
	t.inputFormat, t.inputWrapped, err = inputFormatFor[In]()
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	t.outputFormat, err = outputFormatFor[Out]()
	if err != nil {
		return nil, fmt.Errorf("output schema: %w", err)
	}

	t.handler = func(ctx context.Context, args string) (any, error) {
		var in In
		if t.inputWrapped {
			// Extract wrapped value.
			var decodedArgs inputWrapper[In]
			if err := t.inputFormat.Unmarshal([]byte(args), &decodedArgs); err != nil {
				return nil, err
			}
			in = decodedArgs.Arg0
		} else {
			if err := t.inputFormat.Unmarshal([]byte(args), &in); err != nil {
				return nil, err
			}
		}
		// Call typed handler.
		out, err := h(ctx, in)
		if err != nil {
			return nil, err
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
	inputWrapped bool
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

type inputWrapper[T any] struct {
	Arg0 T
}

func inputFormatFor[T any]() (format *jsonformat.Format, wrapped bool, err error) {
	typ := reflect.TypeFor[T]()
	if typ == reflect.TypeFor[any]() {
		return nil, false, fmt.Errorf("input type any is not supported by HandlerFor; use Handler for dynamic inputs")
	}
	// Dereference pointers so a *Struct input produces the same flat schema as
	// a Struct input (jsonformat.ForType treats pointers equivalently); only
	// genuinely non-struct inputs are wrapped in inputWrapper.
	elem := typ
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		typ = reflect.TypeFor[inputWrapper[T]]()
		wrapped = true
	}
	responseFormat, err := jsonformat.ForType(typ)
	if err != nil {
		return nil, false, err
	}
	format, err = jsonformat.FromResponseFormat(responseFormat)
	return format, wrapped, err
}

func outputFormatFor[T any]() (*jsonformat.Format, error) {
	typ := reflect.TypeFor[T]()
	responseFormat, err := jsonformat.ForType(typ)
	if err != nil {
		return nil, err
	}
	return jsonformat.FromResponseFormat(responseFormat)
}
