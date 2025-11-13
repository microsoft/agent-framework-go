// Copyright (c) Microsoft. All rights reserved.

package functool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework/go/format/jsonformat"
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
	if t.Func.inputFormat == nil {
		// This prevents the tool author from forgetting to write a schema where
		// one should be provided. If we papered over this by supplying the empty
		// schema, then every input would be validated and the problem wouldn't be
		// discovered until runtime, when the LLM sent bad data.
		panic("InputSchema is nil")
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"arg0": t.Func.inputFormat.Schema,
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

	var inzero In
	in, err := jsonformat.NewValue(inzero, nil)
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	t.Func.inputFormat, err = in.FormatJSON()
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}

	var out jsonformat.Value[Out]
	t.Func.outputFormat, err = out.FormatJSON()
	if err != nil {
		return nil, fmt.Errorf("output schema: %w", err)
	}

	t.Handler = func(ctx context.Context, args string) (any, error) {
		if err := in.UnmarshalJSON([]byte(args)); err != nil {
			return nil, err
		}
		// Call typed handler.
		outval, err := h(ctx, in.Unwrap())
		if err != nil {
			return nil, err
		}
		out.Wrap(outval)
		outjson, err := out.MarshalJSON()
		if err != nil {
			return nil, err
		}
		return json.RawMessage(outjson), nil

	} // end of handler

	return &t, nil
}

// Func represents a tool that wraps a Go function to make it callable by AI models.
type Func struct {
	Name        string
	Description string

	inputFormat  *jsonformat.Format
	outputFormat *jsonformat.Format
}
