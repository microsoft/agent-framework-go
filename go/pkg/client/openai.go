// Copyright (c) Microsoft. All rights reserved.

package client

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/pkg/message"
)

// OpenAIChatClient is a ChatClient implementation for OpenAI.
type OpenAIChatClient struct {
	*BaseChatClient
	apiKey   string
	endpoint string
}

// OpenAIChatClientConfig contains configuration for OpenAIChatClient.
type OpenAIChatClientConfig struct {
	APIKey   string
	Model    string
	Endpoint string // Optional, defaults to OpenAI API
}

// NewOpenAIChatClient creates a new OpenAIChatClient.
func NewOpenAIChatClient(config OpenAIChatClientConfig) (*OpenAIChatClient, error) {
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}

	return &OpenAIChatClient{
		BaseChatClient: NewBaseChatClient(config.Model),
		apiKey:         config.APIKey,
		endpoint:       endpoint,
	}, nil
}

// Complete generates a single response for the given messages.
func (c *OpenAIChatClient) Complete(ctx context.Context, options *ChatOptions, messages ...*message.ChatMessage) (*message.ChatResponse, error) {
	// TODO: Implement OpenAI API call
	return &message.ChatResponse{
		Message:      message.NewChatMessage("assistant", "Not implemented"),
		FinishReason: "stop",
		ModelID:      c.ModelID,
	}, nil
}

// CompleteStream generates a streaming response for the given messages.
func (c *OpenAIChatClient) CompleteStream(ctx context.Context, options *ChatOptions, messages ...*message.ChatMessage) iter.Seq2[*message.ChatResponseUpdate, error] {
	return func(yield func(*message.ChatResponseUpdate, error) bool) {
		// TODO: Implement OpenAI streaming API call
	}
}

// AzureOpenAIChatClient is a ChatClient implementation for Azure OpenAI.
type AzureOpenAIChatClient struct {
	*BaseChatClient
	apiKey         string
	endpoint       string
	deploymentName string
}

// AzureOpenAIChatClientConfig contains configuration for AzureOpenAIChatClient.
type AzureOpenAIChatClientConfig struct {
	APIKey         string
	Endpoint       string
	DeploymentName string
	Model          string
}

// NewAzureOpenAIChatClient creates a new AzureOpenAIChatClient.
func NewAzureOpenAIChatClient(config AzureOpenAIChatClientConfig) (*AzureOpenAIChatClient, error) {
	return &AzureOpenAIChatClient{
		BaseChatClient: NewBaseChatClient(config.Model),
		apiKey:         config.APIKey,
		endpoint:       config.Endpoint,
		deploymentName: config.DeploymentName,
	}, nil
}

// Complete generates a single response for the given messages.
func (c *AzureOpenAIChatClient) Complete(ctx context.Context, options *ChatOptions, messages ...*message.ChatMessage) (*message.ChatResponse, error) {
	// TODO: Implement Azure OpenAI API call
	return &message.ChatResponse{
		Message:      message.NewChatMessage("assistant", "Not implemented"),
		FinishReason: "stop",
		ModelID:      c.ModelID,
	}, nil
}

// CompleteStream generates a streaming response for the given messages.
func (c *AzureOpenAIChatClient) CompleteStream(ctx context.Context, options *ChatOptions, messages ...*message.ChatMessage) iter.Seq2[*message.ChatResponseUpdate, error] {
	return func(yield func(*message.ChatResponseUpdate, error) bool) {
		// TODO: Implement Azure OpenAI streaming API call
	}
}
