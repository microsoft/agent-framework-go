// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"encoding/json"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/tool"
)

// AsFuncTool creates a function tool that invokes the given agent.
func (a *Agent) AsFuncTool(options ...agentopt.RunOption) tool.FuncTool {
	return functool{
		name:        a.Name(),
		description: a.Metadata().Description,
		opts:        options,
		agent:       a,
	}
}

type functool struct {
	name        string
	description string
	opts        []agentopt.RunOption
	agent       *Agent
}

func (t functool) Name() string {
	return t.name
}

func (t functool) Description() string {
	return t.description
}

func (t functool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "input query to invoke the agent",
			},
		},
		"required": []string{"query"},
	}
}

func (t functool) ReturnSchema() any {
	return map[string]any{
		"type": "string",
	}
}

func (t functool) Call(ctx context.Context, args string) (any, error) {
	var in struct {
		Query string `json:"query"`
	}
	if args == "" {
		args = "{}"
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return nil, err
	}
	resp, err := t.agent.RunText(in.Query, t.opts...).Collect(ctx)
	if err != nil {
		return "", err
	}
	return resp.String(), nil
}
