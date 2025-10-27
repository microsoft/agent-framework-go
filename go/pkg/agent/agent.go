// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/pkg/tool"
)

// Agent represents an AI agent that can process messages and generate responses.
type Agent[M ~string | any] interface {
	// ID returns the unique identifier.
	ID() string

	// Name returns the name.
	Name() string

	// Run executes the agent with the given messages and options.
	Run(ctx context.Context, thread Thread[M], options *RunOptions, messages ...M) (*RunResponse[M], error)

	// GetNewThread creates a new thread for this agent.
	GetNewThread() Thread[M]

	// DeserializeThread deserializes a thread from JSON.
	DeserializeThread(data []byte) (Thread[M], error)
}

// StreamableAgent is the interface implemented by agents that support streaming responses.
type StreamableAgent[M ~string | any] interface {
	Agent[M]

	// RunStream executes the agent and streams responses.
	RunStream(ctx context.Context, thread Thread[M], options *RunOptions, messages ...M) iter.Seq2[*RunResponseUpdate[M], error]
}

// RunStream is a helper function to run an agent in streaming mode.
// If the agent does not implement [StreamableAgent], it falls back to calling [Agent.Run] sequentially.
func RunStream[M ~string | any](ctx context.Context, agent Agent[M], thread Thread[M], options *RunOptions, messages ...M) iter.Seq2[*RunResponseUpdate[M], error] {
	if agent, ok := agent.(StreamableAgent[M]); ok {
		return agent.RunStream(ctx, thread, options, messages...)
	}
	var tID string
	if thread != nil {
		tID = thread.ID()
	}
	return func(yield func(*RunResponseUpdate[M], error) bool) {
		resp, err := agent.Run(ctx, thread, options, messages...)
		var runResp *RunResponseUpdate[M]
		if resp != nil {
			runResp = &RunResponseUpdate[M]{
				Delta:        resp.Message,
				FinishReason: resp.FinishReason,
				Usage:        resp.Usage,
				ThreadID:     tID,
				ModelID:      resp.ModelID,
			}
		}
		if !yield(runResp, err) {
			return
		}
	}
}

// RunOptions contains options for agent execution.
type RunOptions struct {
	// Tools to make available to the agent.
	Tools []tool.Tool

	// ToolMode specifies how tools should be used.
	ToolMode tool.Mode

	// MaxTurns limits the number of agent turns.
	MaxTurns int

	// Temperature controls randomness in generation.
	Temperature *float64

	// TopP controls nucleus sampling.
	TopP *float64

	// MaxTokens limits the response length.
	MaxTokens *int

	// AdditionalMetadata for provider-specific options.
	AdditionalMetadata map[string]any
}

// RunResponse represents the result of an agent execution.
type RunResponse[M ~string | any] struct {
	Message      M
	FinishReason FinishReason
	Usage        *UsageDetails
	ThreadID     string
	ModelID      string
}

// RunResponseUpdate represents a streaming update from an agent execution.
type RunResponseUpdate[M ~string | any] struct {
	Delta        M
	FinishReason FinishReason
	Usage        *UsageDetails
	ThreadID     string
	ModelID      string
}
