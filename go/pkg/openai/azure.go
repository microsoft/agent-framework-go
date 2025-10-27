// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"github.com/microsoft/agent-framework/go/pkg/agent/chat"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

// AzureChatClientConfig contains configuration for AzureOpenAIChatClient.
type AzureChatClientConfig struct {
	APIKey         string // Optional, if not set will use Azure authentication
	Endpoint       string // Azure OpenAI endpoint (e.g., https://your-resource.openai.azure.com/)
	DeploymentName string // Deployment name for the model
	APIVersion     string // Optional, defaults to latest API version
}

// NewAzureChatClient creates a new AzureOpenAIChatClient.
func NewAzureChatClient(config AzureChatClientConfig) *ChatClient {
	ops := make([]option.RequestOption, 0, 3)

	// Set API version for Azure OpenAI
	apiVersion := config.APIVersion
	if apiVersion == "" {
		// The latest API versions, including previews, can be found here:
		// https://learn.microsoft.com/en-us/azure/ai-services/openai/reference#rest-api-versioning
		apiVersion = "2025-01-01-preview" // Default to latest stable version
	}

	// Configure Azure OpenAI specific settings
	ops = append(ops, azure.WithEndpoint(config.Endpoint, apiVersion))

	// Configure API key if provided
	if config.APIKey != "" {
		ops = append(ops, azure.WithAPIKey(config.APIKey))
	}

	// Create Azure OpenAI client
	client := openai.NewClient(ops...)
	return &ChatClient{
		BaseChatClient: chat.NewBaseChatClient(config.DeploymentName),
		client:         &client,
	}
}
