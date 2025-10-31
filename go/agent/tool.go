// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

type Tool interface {
	ToolInfo() (name string, description string)
}

type CallTool interface {
	Tool

	Schema() any
	Call(ctx context.Context, args map[string]any) (any, error)
}

var _ Tool = (*FuncTool)(nil)
var _ CallTool = (*FuncTool)(nil)

type FuncTool struct {
	Func    Func
	Handler FuncHandler
}

func (t *FuncTool) ToolInfo() (name string, description string) {
	return t.Func.Name, t.Func.Description
}

func (t *FuncTool) Schema() any {
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

func (t *FuncTool) Call(ctx context.Context, args map[string]any) (any, error) {
	if _, ok := args["arg0"]; !ok {
		return nil, fmt.Errorf("missing required argument: arg0")
	}
	argsBytes, err := json.Marshal(args["arg0"])
	if err != nil {
		return nil, err
	}
	return t.Handler(ctx, string(argsBytes))
}

var _ Tool = (*HostedWebSearchTool)(nil)

// HostedWebSearchTool represents a hosted tool that can be specified to an
// AI service to enable it to perform web searches.
//
// This tool does not itself implement web searches. It is a marker that can
// be used to inform a service that the service is allowed to perform web
// searches if the service is capable of doing so.
type HostedWebSearchTool struct {
	Description          string
	AdditionalProperties map[string]any
}

func (t *HostedWebSearchTool) ToolInfo() (name string, description string) {
	return "web_search", t.Description
}

type LoaderTool interface {
	Tool

	LoadTools(ctx context.Context) ([]Tool, error)
}

func loadTools(ctx context.Context, tools []Tool) ([]Tool, error) {
	var result []Tool
	for _, tool := range tools {
		if lt, ok := tool.(LoaderTool); ok {
			innerTools, err := lt.LoadTools(ctx)
			if err != nil {
				name, _ := tool.ToolInfo()
				return nil, fmt.Errorf("failed to load inner tools for %q: %w", name, err)
			}
			result = append(result, innerTools...)
		}
	}
	return result, nil
}

type InitTool interface {
	Tool

	// Init performs any initialization required for the tool.
	Init(ctx context.Context) error
}

// initTools initializes all tools that implement the InitTool interface.
func initTools(ctx context.Context, tools []Tool) error {
	for _, tool := range tools {
		if tool, ok := tool.(InitTool); ok {
			if err := tool.Init(ctx); err != nil {
				name, _ := tool.ToolInfo()
				return fmt.Errorf("failed to initialize tool %q: %w", name, err)
			}
		}
	}
	return nil
}

func ToolString(tool Tool) string {
	name, desc := tool.ToolInfo()
	if desc == "" {
		return fmt.Sprintf("%T{Name: %q}", tool, name)
	}
	return fmt.Sprintf("%T{Name: %q, Description: %q}", tool, name, desc)
}

func runToolCalls(ctx context.Context, options *RunOptions, contents ...Content) []Content {
	if len(options.Tools) == 0 {
		return nil
	}
	funcResults := make(map[string]struct{})
	for _, contents := range contents {
		if funcResult, ok := contents.(*FunctionResultContent); ok {
			funcResults[funcResult.CallID] = struct{}{}
		}
	}
	funcCalls := make([]*FunctionCallContent, 0, len(contents)-len(funcResults))
	for _, contents := range contents {
		if fc, ok := contents.(*FunctionCallContent); ok {
			if _, executed := funcResults[fc.CallID]; executed {
				continue
			}
			funcCalls = append(funcCalls, fc)
		}
	}
	toolContent := make([]Content, 0, len(funcCalls))
	for _, fc := range funcCalls {
		toolContent = append(toolContent, funcCall(ctx, options.Tools, fc))
	}
	return toolContent
}

// funcCall executes a function tool call.
func funcCall(ctx context.Context, tools []Tool, toolCall *FunctionCallContent) (ct Content) {
	if toolCall.Error != nil {
		// If there was an error parsing the tool call, return the error.
		return &FunctionCallContent{
			CallID: toolCall.CallID,
			Error:  toolCall.Error,
		}
	}

	// Find the tool in the options
	var tool CallTool
	for _, t := range tools {
		name, _ := t.ToolInfo()
		if name == toolCall.Name {
			if t, ok := t.(CallTool); ok {
				tool = t
			}
			break
		}
	}

	if tool == nil {
		return &FunctionCallContent{
			CallID: toolCall.CallID,
			Error:  fmt.Errorf("tool not found: %s", toolCall.Name),
		}
	}

	var args map[string]any
	if toolCall.Arguments != "" {
		if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
			return &FunctionCallContent{
				CallID: toolCall.CallID,
				Error:  fmt.Errorf("failed to parse arguments: %w", err),
			}
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
	result, err := tool.Call(ctx, args)
	return &FunctionResultContent{
		CallID: toolCall.CallID,
		Error:  err,
		Result: result,
	}
}
