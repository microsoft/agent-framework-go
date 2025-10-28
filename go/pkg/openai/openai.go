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
	"github.com/openai/openai-go/v3/shared"
)

// ChatClient is a Client implementation for OpenAI.
type ChatClient struct {
	client *openai.Client
	model  string
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
		model:  config.Model,
		client: &client,
	}
}

// NewAgent creates a new agent that uses this chat client.
func (c *ChatClient) NewAgent(instructions string) agent.Agent {
	return chat.New(chat.Config{
		Name:         "OpenAI Chat Agent",
		Instructions: instructions,
		Client:       c,
	})
}

// Complete generates a single response for the given messages.
func (c *ChatClient) Complete(ctx context.Context, options *chat.Options, messages ...*agent.Message) (*chat.Response, error) {
	resp, err := c.client.Chat.Completions.New(ctx, c.buildCompletionParams(options, messages...))
	if err != nil {
		return nil, err
	}
	choice := resp.Choices[0]
	if len(choice.Message.ToolCalls) > 0 {
		// Handle tool calls

	}
	return &chat.Response{
		Message:      agent.NewMessage(agent.Role(choice.Message.Role), &agent.TextContent{Text: choice.Message.Content}),
		FinishReason: agent.FinishReason(choice.FinishReason),
		ModelID:      resp.Model,
	}, nil
}

// CompleteStream generates a streaming response for the given messages.
func (c *ChatClient) CompleteStream(ctx context.Context, options *chat.Options, messages ...*agent.Message) iter.Seq2[*chat.ResponseUpdate, error] {
	stream := c.client.Chat.Completions.NewStreaming(ctx, c.buildCompletionParams(options, messages...))
	return func(yield func(*chat.ResponseUpdate, error) bool) {
		defer stream.Close()
		for stream.Next() {
			current := stream.Current()
			// Skip if no choices are available (common in streaming responses)
			if len(current.Choices) == 0 {
				continue
			}
			choice := current.Choices[0]
			resp := &chat.ResponseUpdate{
				Delta:        agent.NewMessage(agent.Role(choice.Delta.Role), &agent.TextContent{Text: choice.Delta.Content}),
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
func (c *ChatClient) buildCompletionParams(options *chat.Options, messages ...*agent.Message) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    c.model,
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
		for _, tool := range options.Tools {
			params.Tools = append(params.Tools, openai.ChatCompletionToolUnionParam{
				OfFunction: &openai.ChatCompletionFunctionToolParam{
					Function: shared.FunctionDefinitionParam{
						Name:        tool.Name,
						Description: param.NewOpt(tool.Description),
						Parameters:  tool.Schema,
					},
				},
			})
		}
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.String(string(options.ToolMode)),
		}
	}
	return params
}
