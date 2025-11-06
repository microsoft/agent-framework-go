// Copyright (c) Microsoft. All rights reserved.

package agenttest

import (
	"context"

	"github.com/microsoft/agent-framework/go/agent"
)

// Helper types for testing

type InitializableTool struct {
	Tool
	InitFunc func(ctx context.Context) error
}

func (t *InitializableTool) Init(ctx context.Context) error {
	if t.InitFunc != nil {
		return t.InitFunc(ctx)
	}
	return nil
}

type LoaderTool struct {
	Tool
	LoadFunc func(ctx context.Context) ([]agent.Tool, error)
}

func (t *LoaderTool) LoadTools(ctx context.Context) ([]agent.Tool, error) {
	if t.LoadFunc != nil {
		return t.LoadFunc(ctx)
	}
	return nil, nil
}

// Tool is a simple tool for testing.
type Tool struct {
	NameValue   string
	DescValue   string
	CallFunc    func(ctx context.Context, args map[string]any) (any, error)
	SchemaValue any
	CallCount   int
}

func (m *Tool) ToolInfo() (name string, description string) {
	return m.NameValue, m.DescValue
}

func (m *Tool) Schema() any {
	if m.SchemaValue != nil {
		return m.SchemaValue
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{"type": "string"},
		},
	}
}

func (m *Tool) Call(ctx context.Context, args map[string]any) (any, error) {
	m.CallCount++
	if m.CallFunc != nil {
		return m.CallFunc(ctx, args)
	}
	return nil, nil
}
