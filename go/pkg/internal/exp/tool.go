// Copyright (c) Microsoft. All rights reserved.

package exp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework/go/pkg/agent"
)

type LoaderTool interface {
	agent.Tool

	LoadTools(ctx context.Context) ([]agent.Tool, error)
}

func loadTools(ctx context.Context, tools []agent.Tool) ([]agent.Tool, error) {
	var result []agent.Tool
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
	agent.Tool

	// Init performs any initialization required for the tool.
	Init(ctx context.Context) error
}

// initTools initializes all tools that implement the InitTool interface.
func initTools(ctx context.Context, tools []agent.Tool) error {
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

func ToolString(tool agent.Tool) string {
	name, desc := tool.ToolInfo()
	if desc == "" {
		return fmt.Sprintf("%T{Name: %q}", tool, name)
	}
	return fmt.Sprintf("%T{Name: %q, Description: %q}", tool, name, desc)
}

func runToolCalls(ctx context.Context, options *agent.RunOptions, contents ...agent.Content) []agent.Content {
	if len(options.Tools) == 0 {
		return nil
	}
	funcResults := make(map[string]struct{})
	for _, contents := range contents {
		if funcResult, ok := contents.(*agent.FunctionResultContent); ok {
			funcResults[funcResult.CallID] = struct{}{}
		}
	}
	funcCalls := make([]*agent.FunctionCallContent, 0, len(contents)-len(funcResults))
	for _, contents := range contents {
		if fc, ok := contents.(*agent.FunctionCallContent); ok {
			if _, executed := funcResults[fc.CallID]; executed {
				continue
			}
			funcCalls = append(funcCalls, fc)
		}
	}
	toolContent := make([]agent.Content, 0, len(funcCalls))
	for _, fc := range funcCalls {
		toolContent = append(toolContent, funcCall(ctx, options.Tools, fc))
	}
	return toolContent
}

// funcCall executes a function tool call.
func funcCall(ctx context.Context, tools []agent.Tool, toolCall *agent.FunctionCallContent) (ct agent.Content) {
	if toolCall.Error != nil {
		// If there was an error parsing the tool call, return the error.
		return &agent.FunctionCallContent{
			CallID: toolCall.CallID,
			Error:  toolCall.Error,
		}
	}

	// Find the tool in the options
	var tool agent.CallTool
	for _, t := range tools {
		name, _ := t.ToolInfo()
		if name == toolCall.Name {
			if t, ok := t.(agent.CallTool); ok {
				tool = t
			}
			break
		}
	}

	if tool == nil {
		return &agent.FunctionCallContent{
			CallID: toolCall.CallID,
			Error:  fmt.Errorf("tool not found: %s", toolCall.Name),
		}
	}

	var args map[string]any
	if toolCall.Arguments != "" {
		if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
			return &agent.FunctionCallContent{
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
			ct = &agent.FunctionResultContent{
				CallID: toolCall.CallID,
				Error:  fmt.Errorf("tool execution panic: %v", err),
			}
		}
	}()

	// Execute the tool
	result, err := tool.Call(ctx, args)
	return &agent.FunctionResultContent{
		CallID: toolCall.CallID,
		Error:  err,
		Result: result,
	}
}
