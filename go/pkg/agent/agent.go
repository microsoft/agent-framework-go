// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"iter"
)

// Agent represents an AI agent that can process messages and generate responses.
type Agent interface {
	// ID returns the unique identifier.
	ID() string

	// Name returns the name.
	Name() string

	// Run executes the agent with the given messages and options.
	Run(ctx context.Context, thread Thread, options *RunOptions, messages ...*Message) (*RunResponse, error)

	// NewThread creates a new thread for this agent.
	NewThread() Thread

	// DeserializeThread deserializes a thread from JSON.
	DeserializeThread(data []byte) (Thread, error)
}

// StreamableAgent is the interface implemented by agents that support streaming responses.
type StreamableAgent interface {
	Agent

	// RunStream executes the agent and streams responses.
	RunStream(ctx context.Context, thread Thread, options *RunOptions, messages ...*Message) iter.Seq2[*RunResponseUpdate, error]
}

// RunStream is a helper function to run an agent in streaming mode.
// If the agent does not implement [StreamableAgent], it falls back to calling [Agent.Run].
func RunStream(ctx context.Context, agent Agent, thread Thread, options *RunOptions, messages ...*Message) iter.Seq2[*RunResponseUpdate, error] {
	if agent, ok := agent.(StreamableAgent); ok {
		return agent.RunStream(ctx, thread, options, messages...)
	}
	var tID string
	if thread != nil {
		tID = thread.ID()
	}
	return func(yield func(*RunResponseUpdate, error) bool) {
		resp, err := agent.Run(ctx, thread, options, messages...)
		var runResp *RunResponseUpdate
		if resp != nil {
			runResp = &RunResponseUpdate{
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

// Run is a helper function to run an agent with a text message.
func RunText(ctx context.Context, agent Agent, msg string) (*RunResponse, error) {
	return agent.Run(ctx, nil, nil, NewTextMessage(msg))
}

// RunOptions contains options for agent execution.
type RunOptions struct {
	// Tools to make available to the agent.
	Tools []Tool

	// ToolMode specifies how tools should be used.
	ToolMode ToolMode

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
type RunResponse struct {
	Message      *Message
	FinishReason FinishReason
	Usage        *UsageDetails
	ThreadID     string
	ModelID      string
}

// RunResponseUpdate represents a streaming update from an agent execution.
type RunResponseUpdate struct {
	Delta        *Message
	FinishReason FinishReason
	Usage        *UsageDetails
	ThreadID     string
	ModelID      string
}
