// Copyright (c) Microsoft. All rights reserved.

package agenttool

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/tool"
)

// defaultDescription mirrors the .NET AIAgent function-tool default description
// used when an agent exposes no description of its own.
const defaultDescription = "Invoke an agent to retrieve some information."

// invalidNameChars matches every run of characters that are not valid in a
// function name, matching the .NET SanitizeAgentName regex so agent display
// names such as "Weather Agent" become valid provider function names.
var invalidNameChars = regexp.MustCompile(`[^0-9A-Za-z]+`)

// Config represents the configuration for [New].
type Config struct {
	RunOptions []agent.Option
}

// New creates a new FuncTool that invokes the given agent with the provided configuration.
func New(a *agent.Agent, config Config) tool.FuncTool {
	return functool{
		opts:  config.RunOptions,
		agent: a,
	}
}

type functool struct {
	opts  []agent.Option
	agent *agent.Agent
}

func (t functool) Name() string {
	return invalidNameChars.ReplaceAllString(t.agent.Name(), "_")
}

func (t functool) Description() string {
	if d := t.agent.Description(); d != "" {
		return d
	}
	return defaultDescription
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
	resp, err := t.agent.RunText(ctx, in.Query, t.opts...).Collect()
	if err != nil {
		return "", err
	}
	return resp.String(), nil
}
