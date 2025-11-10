package agentext

import (
	"context"

	"github.com/microsoft/agent-framework/go/agent"
)

type CallTool interface {
	agent.Tool

	Schema() any
	Call(ctx context.Context, args map[string]any) (any, error)
}

type InitTool interface {
	agent.Tool

	// Init performs any initialization required for the tool.
	Init(ctx context.Context) error
}

type LoaderTool interface {
	agent.Tool

	LoadTools(ctx context.Context) ([]agent.Tool, error)
}
