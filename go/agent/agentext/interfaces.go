package agentext

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/agent"
)

// StreamableClient is the interface implemented by agents that support streaming responses.
type StreamableClient interface {
	agent.Client

	// RunStream executes the agent and streams responses.
	RunStream(ctx context.Context, thread agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error]
}

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
