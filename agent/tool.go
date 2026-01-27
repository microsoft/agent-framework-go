// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"encoding/json"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/tool"
)

// FuncTool creates a function tool that invokes the given agent.
// The provided session is used for the agent's context during invocations,
// or nil to create a new session for each invocation.
func FuncTool(agent Agent, session memory.Session) tool.FuncTool {
	iden := agent.Identity()
	return functool{
		name:        iden.Name(),
		description: iden.Description(),
		session:     session,
		agent:       agent,
	}
}

type functool struct {
	name        string
	description string
	session     memory.Session
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
	resp, err := RunText(ctx, t.agent, in.Query, agentopt.Session(t.session))
	if err != nil {
		return "", err
	}
	return resp.String(), nil
}
