// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/agentext"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

var _ agent.Client = (*Client)(nil)
var _ agentext.StreamableClient = (*Client)(nil)

type Client struct {
	client openai.Client
	config AgentConfig
}

// AgentConfig contains configuration for [Agent].
type AgentConfig struct {
	Model    string
	APIKey   string // Optional, if not set will use default environment variable
	Endpoint string // Optional, defaults to OpenAI API

	// Only used for Azure OpenAI
	APIVersion string // Optional, defaults to latest API version
}

func newChatClient(isAzure bool, config AgentConfig) *Client {
	ops := make([]option.RequestOption, 0, 2)
	if isAzure {
		if config.Endpoint != "" {
			// The latest API versions, including previews, can be found here:
			// https://learn.microsoft.com/en-us/azure/ai-services/openai/reference#rest-api-versioning
			apiVersion := cmp.Or(config.APIVersion, "2025-01-01-preview")
			ops = append(ops, azure.WithEndpoint(config.Endpoint, apiVersion))
		}
		if config.APIKey != "" {
			ops = append(ops, azure.WithAPIKey(config.APIKey))
		}
	} else {
		if config.APIKey != "" {
			ops = append(ops, option.WithAPIKey(config.APIKey))
		}
		if config.Endpoint != "" {
			ops = append(ops, option.WithBaseURL(config.Endpoint))
		}
	}
	client := openai.NewClient(ops...)
	return &Client{
		client: client,
		config: config,
	}
}

// NewChatClient creates a new Agent.
func NewChatClient(config AgentConfig) *Client {
	return newChatClient(false, config)
}

// NewAzureChatClient creates a new [Agent].
func NewAzureChatClient(config AgentConfig) *Client {
	return newChatClient(true, config)
}

func (a *Client) Run(ctx context.Context, t agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
	resp, err := a.client.Chat.Completions.New(ctx, a.buildCompletionParams(opts, messages...))
	if err != nil {
		return nil, err
	}
	choice := resp.Choices[0]
	contents := make([]agent.Content, 0, 1+len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		contents = append(contents, &agent.FunctionCallContent{
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	if choice.Message.Content != "" {
		contents = append(contents, &agent.TextContent{Text: choice.Message.Content})
	}
	return &agent.RunResponse{
		Messages:   []*agent.Message{agent.NewMessage(agent.Role(choice.Message.Role), contents...)},
		AgentID:    config.ID,
		ResponseID: resp.ID,
	}, nil
}

func (a *Client) RunStream(ctx context.Context, t agent.Thread, config agent.Config, opts *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	stream := a.client.Chat.Completions.NewStreaming(ctx, a.buildCompletionParams(opts, messages...))
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		defer stream.Close()
		var acc openai.ChatCompletionAccumulator
		for stream.Next() {
			chunk := stream.Current()
			if !acc.AddChunk(chunk) {
				continue
			}
			var contents []agent.Content
			if tc, ok := acc.JustFinishedToolCall(); ok {
				contents = append(contents, &agent.FunctionCallContent{
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				})
			}
			if len(chunk.Choices) > 0 {
				if choice := chunk.Choices[0]; choice.Delta.Content != "" {
					contents = append(contents, &agent.TextContent{Text: choice.Delta.Content})
				}
			}
			resp := &agent.RunResponseUpdate{
				Contents:   contents,
				AgentID:    config.ID,
				Role:       agent.RoleAssistant,
				ResponseID: chunk.ID,
				MessageID:  chunk.ID,
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
func (a *Client) buildCompletionParams(options *agent.RunOptions, messages ...*agent.Message) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    a.config.Model,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1),
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
			switch tool := tool.(type) {
			case *agent.HostedWebSearchTool:
				if location, ok := tool.AdditionalProperties["user_location"]; ok {
					if location, ok := location.(map[string]string); ok {
						if city, ok := location["city"]; ok {
							params.WebSearchOptions.UserLocation.Approximate.City = openai.String(city)
						}
						if region, ok := location["region"]; ok {
							params.WebSearchOptions.UserLocation.Approximate.Region = openai.String(region)
						}
						if country, ok := location["country"]; ok {
							params.WebSearchOptions.UserLocation.Approximate.Country = openai.String(country)
						}
						if timezone, ok := location["timezone"]; ok {
							params.WebSearchOptions.UserLocation.Approximate.Timezone = openai.String(timezone)
						}
					}
				}
			case agentext.CallTool:
				name, description := tool.ToolInfo()
				var funcParams map[string]any
				switch schema := tool.Schema().(type) {
				case map[string]any:
					funcParams = schema
				default:
					if schema == nil {
						break
					}
					data, err := json.Marshal(schema)
					if err == nil {
						break
					}
					json.Unmarshal(data, &funcParams)
				}
				params.Tools = append(params.Tools, openai.ChatCompletionToolUnionParam{
					OfFunction: &openai.ChatCompletionFunctionToolParam{
						Function: shared.FunctionDefinitionParam{
							Name:        name,
							Description: openai.String(description),
							Parameters:  funcParams,
						},
					},
				})
			}
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
		var contents []openai.ChatCompletionContentPartTextParam
		for _, content := range msg.Contents {
			if tc, ok := content.(*agent.TextContent); ok {
				contents = append(contents, openai.ChatCompletionContentPartTextParam{
					Text: tc.Text,
				})
			}
		}
		return openai.SystemMessage(contents)
	case agent.RoleUser:
		var contents []openai.ChatCompletionContentPartUnionParam
		for _, content := range msg.Contents {
			switch content := content.(type) {
			case *agent.TextContent:
				contents = append(contents, openai.TextContentPart(content.Text))
			case *agent.URIContent:
				switch topLevelMediaType(content.MediaType) {
				case "image":
					contents = append(contents, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL: content.URI,
					}))
				default:
					// For other URI content types, just ignore, they are not supported yet.
				}
			case *agent.DataContent:
				switch topLevelMediaType(content.MediaType) {
				case "image":
					contents = append(contents, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL: content.URI,
					}))
				case "audio":
					var format string
					switch content.MediaType {
					case "audio/wav":
						format = "wav"
					case "audio/mp3", "audio/mpeg":
						format = "mp3"
					default:
						// Default to mp3
						format = "mp3"
					}
					contents = append(contents, openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
						Data:   string(content.Data),
						Format: format,
					}))
				default:
					contents = append(contents, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
						FileData: openai.String(string(content.Data)),
						Filename: openai.String(content.Name),
					}))
				}
			case *agent.HostedFileContent:
				contents = append(contents, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
					FileID: openai.String(content.FileID),
				}))
			}
		}
		return openai.UserMessage(contents)

	case agent.RoleAssistant:
		var contents []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion
		var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
		for _, content := range msg.Contents {
			switch content := content.(type) {
			case *agent.TextContent:
				contents = append(contents, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
					OfText: &openai.ChatCompletionContentPartTextParam{
						Text: content.Text,
					},
				})
			case *agent.FunctionCallContent:
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: content.CallID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      content.Name,
							Arguments: content.Arguments,
						},
					},
				})
			case *agent.ErrorContent:
				contents = append(contents, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
					OfText: &openai.ChatCompletionContentPartTextParam{
						Text: content.Message,
					},
				})
			}
		}
		return openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content:   openai.ChatCompletionAssistantMessageParamContentUnion{OfArrayOfContentParts: contents},
				ToolCalls: toolCalls,
			},
		}

	case agent.RoleTool:
		var contents []openai.ChatCompletionContentPartTextParam
		var callID string
		for _, content := range msg.Contents {
			switch content := content.(type) {
			case *agent.FunctionResultContent:
				txt := content.Result
				if content.Error != nil {
					txt = content.Error
				}
				contents = append(contents, openai.ChatCompletionContentPartTextParam{
					Text: fmt.Sprintf("%v", txt),
				})
				callID = content.CallID
			}
		}
		return openai.ToolMessage(contents, callID)

	default:
		panic("unknown message role: " + string(msg.Role))
	}
}

func topLevelMediaType(media string) string {
	if media == "" {
		return ""
	}
	if idx := strings.Index(media, "/"); idx >= 0 {
		return media[:idx]
	}
	return media
}
