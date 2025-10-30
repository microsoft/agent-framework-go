// Copyright (c) Microsoft. All rights reserved.

package exp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework/go/pkg/agent"
)

func ToolString(tool agent.Tool) string {
	name, desc := tool.ToolInfo()
	if desc == "" {
		return fmt.Sprintf("%T{Name: %q}", tool, name)
	}
	return fmt.Sprintf("%T{Name: %q, Description: %q}", tool, name, desc)
}

func RunToolCalls(ctx context.Context, options *agent.RunOptions, contents ...agent.Content) []agent.Content {
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
		if funcCall, ok := contents.(*agent.FunctionCallContent); ok {
			if _, executed := funcResults[funcCall.CallID]; executed {
				continue
			}
			funcCalls = append(funcCalls, funcCall)
		}
	}
	toolContent := make([]agent.Content, 0, len(funcCalls))
	for _, funcCall := range funcCalls {
		toolContent = append(toolContent, FuncCall(ctx, options.Tools, funcCall))
	}
	return toolContent
}

// FuncCall executes a function tool call.
func FuncCall(ctx context.Context, tools []agent.Tool, toolCall *agent.FunctionCallContent) (ct agent.Content) {
	if toolCall.Error != nil {
		// If there was an error parsing the tool call, return the error.
		return &agent.FunctionCallContent{
			CallID: toolCall.CallID,
			Error:  toolCall.Error,
		}
	}

	// Find the tool in the options
	var tool *agent.Func
	for _, t := range tools {
		fn, ok := t.(*agent.Func)
		if !ok {
			continue
		}
		if fn.Name == toolCall.Name {
			tool = fn
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
