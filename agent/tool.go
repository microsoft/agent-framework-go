// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"encoding/json"

	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/tool"
)

// AsFuncTool creates a function tool that invokes the given agent.
func (a *Agent) AsFuncTool(options ...agentopt.Option) tool.FuncTool {
	return functool{
		name:        a.Name(),
		description: a.Description(),
		opts:        options,
		agent:       a,
	}
}

type functool struct {
	name        string
	description string
	opts        []agentopt.Option
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

func (t functool) Call(ctx tool.Context, args string) (any, error) {
	var in struct {
		Query string `json:"query"`
	}
	if args == "" {
		args = "{}"
	}
	if err := json.Unmarshal([]byte(args), &in); err != nil {
		return nil, err
	}
	resp, err := t.agent.RunText(ctx, in.Query, t.opts...).Collect()
	if err != nil {
		return "", err
	}
	return resp.String(), nil
}
