// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
)

// ToolMode represents how tools should be used by the agent.
type ToolMode string

const (
	// ToolModeAuto allows the agent to decide when to use tools.
	ToolModeAuto ToolMode = "auto"
	// ToolModeRequired forces the agent to use at least one tool.
	ToolModeRequired ToolMode = "required"
	// ToolModeNone disables tool usage.
	ToolModeNone ToolMode = "none"
)

// Tool represents a tool or function that an agent can use.
type Tool struct {
	Name        string
	Description string

	fn          any
	wantContext bool
	hasError    bool
	schema      map[string]any
	argsOrder   []string
}

var (
	typeOfContext = reflect.TypeOf((*context.Context)(nil)).Elem()
	typeOfError   = reflect.TypeOf((*error)(nil)).Elem()
)

func NewTool(name string, fn any) Tool {
	fnType := reflect.TypeOf(fn)
	if fnType.Kind() != reflect.Func {
		panic("tool function must be a function")
	}

	var hasError bool
	switch fnType.NumOut() {
	case 0:
		// no return values
	case 1:
		// one return value, check if it's error
		hasError = fnType.Out(0).Implements(typeOfError)
	case 2:
		// two return values, second must be error
		if !fnType.Out(1).Implements(typeOfError) {
			panic("tool function's second return value must be of type error")
		}
		hasError = true
	default:
		panic("tool function must have at most two return values")
	}

	tool := Tool{
		Name:      name,
		fn:        fn,
		hasError:  hasError,
		argsOrder: make([]string, 0, fnType.NumIn()),
	}
	var args = make(map[string]any, fnType.NumIn())
	for i := 0; i < fnType.NumIn(); i++ {
		typ := fnType.In(i)
		if i == 0 && typ == typeOfContext {
			tool.wantContext = true
			continue
		}
		name := "arg" + strconv.Itoa(i)
		args[name] = map[string]any{
			"type": typ.String(),
		}
		tool.argsOrder = append(tool.argsOrder, name)

	}
	tool.schema = map[string]any{
		"type":       "object",
		"properties": args,
	}
	return tool
}

func (t *Tool) Schema() map[string]any {
	return t.schema
}

func (t *Tool) Call(ctx context.Context, args map[string]any) (any, error) {
	fnValue := reflect.ValueOf(t.fn)
	in := make([]reflect.Value, 0, len(args)+1)
	if t.wantContext {
		in = append(in, reflect.ValueOf(ctx))
	}
	for _, name := range t.argsOrder {
		arg, ok := args[name]
		if !ok {
			return nil, fmt.Errorf("missing argument: %s", name)
		}
		in = append(in, reflect.ValueOf(arg))
	}
	out := fnValue.Call(in)
	switch len(out) {
	case 0:
		return nil, nil
	case 1:
		if t.hasError {
			if !out[0].IsNil() {
				return nil, out[0].Interface().(error)
			}
			return nil, nil
		}
		return out[0].Interface(), nil
	case 2:
		var err error
		if t.hasError {
			if !out[1].IsNil() {
				err = out[1].Interface().(error)
			}
		}
		return out[0].Interface(), err
	default:
		panic("unexpected number of return values")
	}
}

// CallTools executes the given tool calls using the provided tools.
func CallTools(ctx context.Context, tools []Tool, contents ...Content) *Message {
	// Execute all tool calls and collect results
	toolResults := make([]Content, 0, len(contents))
	for _, content := range contents {
		toolCall, ok := content.(*FunctionCallContent)
		if !ok {
			continue
		}
		result := executeTool(ctx, tools, toolCall)
		toolResults = append(toolResults, result)
	}

	return NewMessage(RoleTool, toolResults...)
}

// executeTool executes a single tool call.
func executeTool(ctx context.Context, tools []Tool, toolCall *FunctionCallContent) (ct Content) {
	if toolCall.Error != nil {
		// If there was an error parsing the tool call, return the error.
		return &FunctionCallContent{
			CallID: toolCall.CallID,
			Error:  toolCall.Error,
		}
	}

	// Find the tool in the options
	var tool *Tool
	for _, t := range tools {
		if t.Name == toolCall.Name {
			tool = &t
			break
		}
	}

	if tool == nil {
		return &FunctionCallContent{
			CallID: toolCall.CallID,
			Error:  fmt.Errorf("tool not found: %s", toolCall.Name),
		}
	}

	defer func() {
		if r := recover(); r != nil {
			var err error
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
			ct = &FunctionResultContent{
				CallID: toolCall.CallID,
				Error:  fmt.Errorf("tool execution panic: %v", err),
			}
		}
	}()

	// Execute the tool
	result, err := tool.Call(ctx, toolCall.Arguments)
	return &FunctionResultContent{
		CallID: toolCall.CallID,
		Error:  err,
		Result: result,
	}
}
