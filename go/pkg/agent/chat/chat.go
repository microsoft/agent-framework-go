// Copyright (c) Microsoft. All rights reserved.

package chat

import (
	"context"
	"iter"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework/go/pkg/agent"
)

var _ agent.Agent[*Message] = (*Agent)(nil) // ensure Agent implements Agent interface

// Agent is an agent that uses a [Client] to generate responses.
type Agent struct {
	id           string
	name         string
	instructions string
	client       Client
}

// Config contains configuration for creating a [Agent].
type Config struct {
	Name         string
	Instructions string
	Client       Client
}

// New creates a new [Agent].
func New(config Config) *Agent {
	return &Agent{
		id:           uuid.New().String(),
		name:         config.Name,
		instructions: config.Instructions,
		client:       config.Client,
	}
}

// ID returns the agent's unique identifier.
func (a *Agent) ID() string {
	return a.id
}

// Name returns the agent's name.
func (a *Agent) Name() string {
	return a.name
}

// Run executes the agent with the given messages and options.
func (a *Agent) Run(ctx context.Context, t agent.Thread[*Message], options *agent.RunOptions, messages ...*Message) (*agent.RunResponse[*Message], error) {
	// Prepare messages with system instructions
	allMessages := a.prepareMessages(messages)

	// Convert RunOptions to ChatOptions
	chatOptions := a.convertOptions(options)

	// Call the chat client
	response, err := a.client.Complete(ctx, chatOptions, allMessages...)
	if err != nil {
		return nil, err
	}

	// Update thread if provided
	if t != nil {
		for _, msg := range messages {
			t.AddMessage(msg)
		}
		t.AddMessage(response.Message)
	}

	// Convert to RunResponse
	return &agent.RunResponse[*Message]{
		Message:      response.Message,
		FinishReason: response.FinishReason,
		Usage:        response.Usage,
		ThreadID:     getThreadID(t),
		ModelID:      response.ModelID,
	}, nil
}

// RunStream executes the agent and streams responses.
func (a *Agent) RunStream(ctx context.Context, t agent.Thread[*Message], options *agent.RunOptions, messages ...*Message) iter.Seq2[*agent.RunResponseUpdate[*Message], error] {
	// Prepare messages with system instructions
	allMessages := a.prepareMessages(messages)

	// Convert RunOptions to ChatOptions
	chatOptions := a.convertOptions(options)

	// Call the chat client for streaming
	tID := getThreadID(t)
	return func(yield func(*agent.RunResponseUpdate[*Message], error) bool) {
		for resp, err := range completeStream(ctx, a.client, chatOptions, allMessages...) {
			var runResp *agent.RunResponseUpdate[*Message]
			if resp != nil {
				runResp = &agent.RunResponseUpdate[*Message]{
					Delta:        resp.Delta,
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
}

// GetNewThread creates a new thread for this agent.
func (a *Agent) GetNewThread() agent.Thread[*Message] {
	return agent.NewInMemoryThread[*Message]()
}

// DeserializeThread deserializes a thread from JSON.
func (a *Agent) DeserializeThread(data []byte) (agent.Thread[*Message], error) {
	// TODO: Implement JSON deserialization
	return agent.NewInMemoryThread[*Message](), nil
}

// prepareMessages adds system instructions to the message list.
func (a *Agent) prepareMessages(messages []*Message) []*Message {
	if a.instructions == "" {
		return messages
	}

	systemMessage := NewMessage("system", a.instructions)
	allMessages := make([]*Message, 0, len(messages)+1)
	allMessages = append(allMessages, systemMessage)
	allMessages = append(allMessages, messages...)
	return allMessages
}

// convertOptions converts RunOptions to ChatOptions.
func (a *Agent) convertOptions(options *agent.RunOptions) *Options {
	if options == nil {
		return nil
	}

	return &Options{
		Tools:              options.Tools,
		ToolMode:           options.ToolMode,
		Temperature:        options.Temperature,
		TopP:               options.TopP,
		MaxTokens:          options.MaxTokens,
		AdditionalMetadata: options.AdditionalMetadata,
	}
}

// getThreadID returns the thread ID or empty string if no thread.
func getThreadID(t agent.Thread[*Message]) string {
	if t == nil {
		return ""
	}
	return t.ID()
}
