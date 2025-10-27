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
	Model    string
	APIKey   string // Optional, if not set will use default environment variable
	Endpoint string // Optional, defaults to OpenAI API
}

// NewChatClient creates a new OpenAIChatClient.
func NewChatClient(config ChatClientConfig) *ChatClient {
	ops := make([]option.RequestOption, 0, 2)
	if config.APIKey != "" {
		ops = append(ops, option.WithAPIKey(config.APIKey))
	}
	if config.Endpoint != "" {
		ops = append(ops, option.WithBaseURL(config.Endpoint))
	}
	client := openai.NewClient(ops...)
	return &ChatClient{
		BaseChatClient: chat.NewBaseChatClient(config.Model),
		client:         &client,
	}
}

// NewAgent creates a new agent that uses this chat client.
func (c *ChatClient) NewAgent(instructions string) agent.Agent[*chat.Message] {
	return chat.New(chat.Config{
		Name:         "OpenAI Chat Agent",
		Instructions: instructions,
		Client:       c,
	})
}

// Complete generates a single response for the given messages.
func (c *ChatClient) Complete(ctx context.Context, options *chat.Options, messages ...*chat.Message) (*chat.Response, error) {
	resp, err := c.client.Chat.Completions.New(ctx, c.buildCompletionParams(options, messages...))
	if err != nil {
		return nil, err
	}
	choice := resp.Choices[0]
	return &chat.Response{
		Message:      chat.NewMessage(agent.Role(choice.Message.Role), choice.Message.Content),
		FinishReason: agent.FinishReason(choice.FinishReason),
		ModelID:      resp.Model,
	}, nil
}

// CompleteStream generates a streaming response for the given messages.
func (c *ChatClient) CompleteStream(ctx context.Context, options *chat.Options, messages ...*chat.Message) iter.Seq2[*chat.ResponseUpdate, error] {
	stream := c.client.Chat.Completions.NewStreaming(ctx, c.buildCompletionParams(options, messages...))
	return func(yield func(*chat.ResponseUpdate, error) bool) {
		defer stream.Close()
		for stream.Next() {
			choice := stream.Current().Choices[0]
			resp := &chat.ResponseUpdate{
				Delta:        chat.NewMessage(agent.Role(choice.Delta.Role), choice.Delta.Content),
				FinishReason: agent.FinishReason(choice.FinishReason),
			}
			if !yield(resp, nil) {
				return
			}
		}
		if stream.Err() != nil {
			yield(nil, stream.Err())
		}
	}
}

// buildCompletionParams constructs the parameters for the OpenAI chat completion API.
func (c *ChatClient) buildCompletionParams(options *chat.Options, messages ...*chat.Message) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    c.ModelID,
		N:        openai.Int(1),
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)),
	}
	for _, msg := range messages {
		// TODO: support roles, content types, and multiple messages
		params.Messages = append(params.Messages, openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: param.NewOpt(msg.Text()),
				},
			},
		})
	}
	if options != nil {
		if options.Temperature != nil {
			params.Temperature = openai.Float(*options.Temperature)
		}
		if options.TopP != nil {
			params.TopP = openai.Float(*options.TopP)
		}
		if options.MaxTokens != nil {
			params.MaxTokens = openai.Int(int64(*options.MaxTokens))
		}
	}

	return params
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
