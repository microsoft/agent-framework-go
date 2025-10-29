// Copyright (c) Microsoft. All rights reserved.

package exp

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework/go/pkg/agent"
)

// CallTools executes the given tool calls using the provided tools.
func CallTools(ctx context.Context, tools []*agent.Tool, contents ...agent.Content) *agent.Message {
	// Execute all tool calls and collect results
	toolResults := make([]agent.Content, 0, len(contents))
	for _, content := range contents {
		toolCall, ok := content.(*agent.FunctionCallContent)
		if !ok {
			continue
		}
		result := executeTool(ctx, tools, toolCall)
		toolResults = append(toolResults, result)
	}

	return agent.NewMessage(agent.RoleTool, toolResults...)
}

// executeTool executes a single tool call.
func executeTool(ctx context.Context, tools []*agent.Tool, toolCall *agent.FunctionCallContent) (ct agent.Content) {
	if toolCall.Error != nil {
		// If there was an error parsing the tool call, return the error.
		return &agent.FunctionCallContent{
			CallID: toolCall.CallID,
			Error:  toolCall.Error,
		}
	}

	// Find the tool in the options
	var tool *agent.Tool
	for _, t := range tools {
		if t.Name == toolCall.Name {
			tool = t
			break
		}
	}

	if tool == nil {
		return &agent.FunctionCallContent{
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
			ct = &agent.FunctionResultContent{
				CallID: toolCall.CallID,
				Error:  fmt.Errorf("tool execution panic: %v", err),
			}
		}
	}()

	// Execute the tool
	result, err := tool.Call(ctx, toolCall.Arguments)
	return &agent.FunctionResultContent{
		CallID: toolCall.CallID,
		Error:  err,
		Result: result,
	}
}
