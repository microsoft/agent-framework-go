// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework/golang/pkg/client"
	"github.com/microsoft/agent-framework/golang/pkg/message"
	"github.com/microsoft/agent-framework/golang/pkg/thread"
)

// ChatAgent is an agent that uses a ChatClient to generate responses.
type ChatAgent struct {
	id           string
	name         string
	instructions string
	chatClient   client.ChatClient
}

// ChatAgentConfig contains configuration for creating a ChatAgent.
type ChatAgentConfig struct {
	Name         string
	Instructions string
	ChatClient   client.ChatClient
}

// NewChatAgent creates a new ChatAgent.
func NewChatAgent(config ChatAgentConfig) *ChatAgent {
	return &ChatAgent{
		id:           uuid.New().String(),
		name:         config.Name,
		instructions: config.Instructions,
		chatClient:   config.ChatClient,
	}
}

// ID returns the agent's unique identifier.
func (a *ChatAgent) ID() string {
	return a.id
}

// Name returns the agent's name.
func (a *ChatAgent) Name() string {
	return a.name
}

// Run executes the agent with the given messages and options.
func (a *ChatAgent) Run(ctx context.Context, messages []*message.ChatMessage, t thread.AgentThread, options *RunOptions) (*RunResponse, error) {
	// Prepare messages with system instructions
	allMessages := a.prepareMessages(messages)

	// Convert RunOptions to ChatOptions
	chatOptions := a.convertOptions(options)

	// Call the chat client
	response, err := a.chatClient.Complete(ctx, allMessages, chatOptions)
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
	return &RunResponse{
		Message:      response.Message,
		FinishReason: response.FinishReason,
		Usage:        response.Usage,
		ThreadID:     a.getThreadID(t),
		ModelID:      response.ModelID,
	}, nil
}

// RunStream executes the agent and streams responses.
func (a *ChatAgent) RunStream(ctx context.Context, messages []*message.ChatMessage, t thread.AgentThread, options *RunOptions) (<-chan *RunResponseUpdate, error) {
	// Prepare messages with system instructions
	allMessages := a.prepareMessages(messages)

	// Convert RunOptions to ChatOptions
	chatOptions := a.convertOptions(options)

	// Call the chat client for streaming
	responseChan, err := a.chatClient.CompleteStream(ctx, allMessages, chatOptions)
	if err != nil {
		return nil, err
	}

	// Convert chat responses to run responses
	runResponseChan := make(chan *RunResponseUpdate)
	go func() {
		defer close(runResponseChan)
		for update := range responseChan {
			runResponseChan <- &RunResponseUpdate{
				Delta:        update.Delta,
				FinishReason: update.FinishReason,
				Usage:        update.Usage,
				ThreadID:     a.getThreadID(t),
				ModelID:      update.ModelID,
			}
		}
	}()

	return runResponseChan, nil
}

// GetNewThread creates a new thread for this agent.
func (a *ChatAgent) GetNewThread() thread.AgentThread {
	return thread.NewInMemoryThread()
}

// DeserializeThread deserializes a thread from JSON.
func (a *ChatAgent) DeserializeThread(data []byte) (thread.AgentThread, error) {
	// TODO: Implement JSON deserialization
	return thread.NewInMemoryThread(), nil
}

// prepareMessages adds system instructions to the message list.
func (a *ChatAgent) prepareMessages(messages []*message.ChatMessage) []*message.ChatMessage {
	if a.instructions == "" {
		return messages
	}

	systemMessage := message.NewChatMessage("system", a.instructions)
	allMessages := make([]*message.ChatMessage, 0, len(messages)+1)
	allMessages = append(allMessages, systemMessage)
	allMessages = append(allMessages, messages...)
	return allMessages
}

// convertOptions converts RunOptions to ChatOptions.
func (a *ChatAgent) convertOptions(options *RunOptions) *client.ChatOptions {
	if options == nil {
		return nil
	}

	return &client.ChatOptions{
		Tools:              options.Tools,
		ToolMode:           options.ToolMode,
		Temperature:        options.Temperature,
		TopP:               options.TopP,
		MaxTokens:          options.MaxTokens,
		AdditionalMetadata: options.AdditionalMetadata,
	}
}

// getThreadID returns the thread ID or empty string if no thread.
func (a *ChatAgent) getThreadID(t thread.AgentThread) string {
	if t == nil {
		return ""
	}
	return t.ID()
}
