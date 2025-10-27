// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"context"
	"iter"

	"github.com/microsoft/agent-framework/go/pkg/agent"
	"github.com/microsoft/agent-framework/go/pkg/agent/chat"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

// ChatClient is a Client implementation for OpenAI.
type ChatClient struct {
	*chat.BaseChatClient
	client *openai.Client
}

// ChatClientConfig contains configuration for OpenAIChatClient.
type ChatClientConfig struct {
	APIKey   string
	Model    string
	Endpoint string // Optional, defaults to OpenAI API
}

// NewOpenAIChatClient creates a new OpenAIChatClient.
func NewOpenAIChatClient(config ChatClientConfig) (*ChatClient, error) {
	ops := []option.RequestOption{option.WithAPIKey(config.APIKey)}
	if config.Endpoint != "" {
		ops = append(ops, option.WithBaseURL(config.Endpoint))
	}
	client := openai.NewClient(ops...)
	return &ChatClient{
		BaseChatClient: chat.NewBaseChatClient(config.Model),
		client:         &client,
	}, nil
}

// Complete generates a single response for the given messages.
func (c *ChatClient) Complete(ctx context.Context, options *chat.Options, messages ...*chat.Message) (*chat.Response, error) {
	oaiMsgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, msg := range messages {
		// TODO: support roles, content types, and multiple messages
		oaiMsgs = append(oaiMsgs, openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: param.NewOpt(msg.Text()),
				},
			},
		})
	}
	resp, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    c.ModelID,
		N:        openai.Int(1),
		Messages: oaiMsgs,
	})
	if err != nil {
		return nil, err
	}
	choise := resp.Choices[0]
	return &chat.Response{
		Message:      chat.NewMessage(agent.Role(choise.Message.Role), choise.Message.Content),
		FinishReason: agent.FinishReason(choise.FinishReason),
		ModelID:      resp.Model,
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
