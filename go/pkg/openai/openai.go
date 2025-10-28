// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"context"
	"encoding/json"
	"fmt"
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
func (c *ChatClient) NewAgent(config *chat.Config) agent.Agent {
	return chat.New(c, config)
}

// Complete generates a single response for the given messages.
func (c *ChatClient) Complete(ctx context.Context, options *chat.Options, messages ...*agent.Message) (*chat.Response, error) {
	// Keep track of all messages in the conversation
	completionParams := buildCompletionParams(c.model, options, messages...)

	for {
		// Call OpenAI API
		resp, err := c.client.Chat.Completions.New(ctx, completionParams)
		if err != nil {
			return nil, err
		}

		choice := resp.Choices[0]

		// Check if there are tool calls to execute
		if choice.FinishReason == "tool_calls" && len(choice.Message.ToolCalls) > 0 {
			// Parse and collect tool calls
			assistant, tools := processToolCalls(ctx, choice, options)
			completionParams.Messages = append(completionParams.Messages, buildMessageParam(assistant), buildMessageParam(tools))

			// Continue the loop to get the final response
			continue
		}

		// If no tool calls, this is the final response
		contents := make([]agent.Content, 0, 1)
		if choice.Message.Content != "" {
			contents = append(contents, &agent.TextContent{Text: choice.Message.Content})
		} else {
			contents = append(contents, &agent.TextContent{Text: ""})
		}

		return &chat.Response{
			Message:      agent.NewMessage(agent.Role(choice.Message.Role), contents...),
			FinishReason: agent.FinishReason(choice.FinishReason),
			ModelID:      resp.Model,
		}, nil
	}
}

// CompleteStream generates a streaming response for the given messages.
func (c *ChatClient) CompleteStream(ctx context.Context, options *chat.Options, messages ...*agent.Message) iter.Seq2[*chat.ResponseUpdate, error] {
	stream := c.client.Chat.Completions.NewStreaming(ctx, buildCompletionParams(c.model, options, messages...))
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

func processToolCalls(ctx context.Context, choice openai.ChatCompletionChoice, options *chat.Options) (assistant, tools *agent.Message) {
	toolCalls := make([]agent.Content, 0, len(choice.Message.ToolCalls))
	for _, toolCall := range choice.Message.ToolCalls {
		// Parse arguments from JSON string to map
		var args map[string]any
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
			// If parsing fails, store the error
			toolCalls = append(toolCalls, &agent.FunctionCallContent{
				CallID: toolCall.ID,
				Name:   toolCall.Function.Name,
				Error:  err,
			})
			continue
		}

		toolCalls = append(toolCalls, &agent.FunctionCallContent{
			CallID:    toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: args,
		})
	}

	assistant = agent.NewMessage(agent.RoleAssistant, toolCalls...)
	tools = agent.CallTools(ctx, options.Tools, toolCalls...)
	return assistant, tools
}

// buildCompletionParams constructs the parameters for the OpenAI chat completion API.
func buildCompletionParams(model string, options *chat.Options, messages ...*agent.Message) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    model,
		N:        openai.Int(1),
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)),
	}

	for _, msg := range messages {
		params.Messages = append(params.Messages, buildMessageParam(msg))
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
						Parameters:  tool.Schema(),
					},
				},
			})
		}
		if options.ToolMode != "" {
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String(string(options.ToolMode)),
			}
		}
	}
	return params
}

// buildMessageParam converts an agent.Message to an OpenAI message parameter.
func buildMessageParam(msg *agent.Message) openai.ChatCompletionMessageParamUnion {
	switch msg.Role {
	case agent.RoleSystem:
		return openai.ChatCompletionMessageParamUnion{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: param.NewOpt(extractText(msg)),
				},
			},
		}

	case agent.RoleUser:
		return openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: param.NewOpt(extractText(msg)),
				},
			},
		}

	case agent.RoleAssistant:
		// Check if the message contains tool calls
		toolCalls := extractToolCalls(msg)
		return openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: param.NewOpt(extractText(msg)),
				},
				ToolCalls: toolCalls,
			},
		}

	case agent.RoleTool:
		// Tool messages contain function results
		toolResults := extractToolResults(msg)
		return openai.ChatCompletionMessageParamUnion{
			OfTool: &openai.ChatCompletionToolMessageParam{
				Content: openai.ChatCompletionToolMessageParamContentUnion{
					OfString: param.NewOpt(toolResults),
				},
				ToolCallID: extractToolCallID(msg),
			},
		}

	default:
		panic("unknown message role: " + string(msg.Role))
	}
}

// extractText extracts text content from a message.
func extractText(msg *agent.Message) string {
	var text string
	for _, content := range msg.Contents {
		if tc, ok := content.(*agent.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

// extractToolCalls extracts function call content from a message.
func extractToolCalls(msg *agent.Message) []openai.ChatCompletionMessageToolCallUnionParam {
	var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
	for _, content := range msg.Contents {
		if fc, ok := content.(*agent.FunctionCallContent); ok {
			// Marshal arguments to JSON
			argsJSON, err := json.Marshal(fc.Arguments)
			if err != nil {
				continue
			}

			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: fc.CallID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      fc.Name,
						Arguments: string(argsJSON),
					},
				},
			})
		}
	}
	return toolCalls
}

// extractToolResults extracts function result content from a message and formats it.
func extractToolResults(msg *agent.Message) string {
	for _, content := range msg.Contents {
		if fr, ok := content.(*agent.FunctionResultContent); ok {
			if fr.Error != nil {
				return fmt.Sprintf("Error: %v", fr.Error)
			}
			return fmt.Sprintf("%v", fr.Result)
		}
	}
	return ""
}

// extractToolCallID extracts the first tool call ID from function result content.
func extractToolCallID(msg *agent.Message) string {
	for _, content := range msg.Contents {
		if fr, ok := content.(*agent.FunctionResultContent); ok {
			return fr.CallID
		}
	}
	return ""
}
