// Code generated from agent/agentext/interfaces.go by update.go. DO NOT EDIT.

package agent

import (
	"context"
	"iter"
)

// StreamableClient is the interface implemented by agents that support streaming responses.
type streamableClient interface {
	Client

	// RunStream executes the agent and streams responses.
	RunStream(ctx context.Context, thread Thread, config Config, opts *RunOptions, messages ...*Message) iter.Seq2[*RunResponseUpdate, error]
}

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
