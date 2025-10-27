// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/pkg/agent/chat"
)

// ChatClient is a Client implementation for OpenAI.
type ChatClient struct {
	*chat.BaseChatClient
	apiKey   string
	endpoint string
}

// ChatClientConfig contains configuration for OpenAIChatClient.
type ChatClientConfig struct {
	APIKey   string
	Model    string
	Endpoint string // Optional, defaults to OpenAI API
}

// NewOpenAIChatClient creates a new OpenAIChatClient.
func NewOpenAIChatClient(config ChatClientConfig) (*ChatClient, error) {
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}

	return &ChatClient{
		BaseChatClient: chat.NewBaseChatClient(config.Model),
		apiKey:         config.APIKey,
		endpoint:       endpoint,
	}, nil
}

// Complete generates a single response for the given messages.
func (c *ChatClient) Complete(ctx context.Context, options *chat.Options, messages ...*chat.Message) (*chat.Response, error) {
	// TODO: Implement OpenAI API call
	return &chat.Response{
		Message:      chat.NewMessage("assistant", "Not implemented"),
		FinishReason: "stop",
		ModelID:      c.ModelID,
	}, nil
}

// CompleteStream generates a streaming response for the given messages.
func (c *ChatClient) CompleteStream(ctx context.Context, options *chat.Options, messages ...*chat.Message) iter.Seq2[*chat.ResponseUpdate, error] {
	return func(yield func(*chat.ResponseUpdate, error) bool) {
		// TODO: Implement OpenAI streaming API call
	}
}

// AzureOpenAIChatClient is a ChatClient implementation for Azure OpenAI.
type AzureOpenAIChatClient struct {
	*chat.BaseChatClient
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
		BaseChatClient: chat.NewBaseChatClient(config.Model),
		apiKey:         config.APIKey,
		endpoint:       config.Endpoint,
		deploymentName: config.DeploymentName,
	}, nil
}

// Complete generates a single response for the given messages.
func (c *AzureOpenAIChatClient) Complete(ctx context.Context, options *chat.Options, messages ...*chat.Message) (*chat.Response, error) {
	// TODO: Implement Azure OpenAI API call
	return &chat.Response{
		Message:      chat.NewMessage("assistant", "Not implemented"),
		FinishReason: "stop",
		ModelID:      c.ModelID,
	}, nil
}

// CompleteStream generates a streaming response for the given messages.
func (c *AzureOpenAIChatClient) CompleteStream(ctx context.Context, options *chat.Options, messages ...*chat.Message) iter.Seq2[*chat.ResponseUpdate, error] {
	return func(yield func(*chat.ResponseUpdate, error) bool) {
		// TODO: Implement Azure OpenAI streaming API call
	}
}
