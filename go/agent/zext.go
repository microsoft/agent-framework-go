// Code generated from agent/agentext/interfaces.go by update.go. DO NOT EDIT.

package agent

import (
	"context"
)

type callTool interface {
	Tool

	Schema() any
	Call(ctx context.Context, args map[string]any) (any, error)
}

type initTool interface {
	Tool

	// Init performs any initialization required for the tool.
	Init(ctx context.Context) error
}

type loaderTool interface {
	Tool

	LoadTools(ctx context.Context) ([]Tool, error)
}
