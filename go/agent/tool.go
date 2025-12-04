// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
)

// FuncTool creates a function tool that invokes the given agent.
// The provided thread is used for the agent's context during invocations,
// or nil to create a new thread for each invocation.
func FuncTool(agent Agent, thread memory.Thread) tool.FuncTool {
	iden := agent.Identity()
	return functool{
		name:        iden.Name(),
		description: iden.Description(),
		thread:      thread,
		agent:       agent,
	}
}

type functool struct {
	name        string
	description string
	thread      memory.Thread
	agent       Agent
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

func (t functool) Call(ctx context.Context, args any) (any, error) {
	var in struct {
		Query string `json:"query"`
	}
	var raw json.RawMessage
	if args == nil {
		raw = json.RawMessage("{}")
	} else {
		var ok bool
		raw, ok = args.(json.RawMessage)
		if !ok {
			return nil, fmt.Errorf("expected json.RawMessage arguments, got %T", args)
		}
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	resp, err := Run(ctx, t.agent, RunOptions{Thread: t.thread}, &message.Message{
		Role:     message.RoleUser,
		Contents: []message.Content{&message.TextContent{Text: in.Query}},
	})
	if err != nil {
		return "", err
	}
	return resp.String(), nil
}
