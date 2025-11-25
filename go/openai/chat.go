// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"time"

	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework/go/format"
	"github.com/microsoft/agent-framework/go/format/jsonformat"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/hostedtool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// NewWebSearchTool creates a new [hostedtool.WebSearch] with the specified user location.
// All parameters are optional; pass empty strings for any unknown values.
//
// SearchContextSize is the high level guidance for the amount of context window space to use for the
// search. One of `low`, `medium`, or `high`. `medium` is the default.
func NewWebSearchTool(city, region, country, timezone, searchContextSize string) *hostedtool.WebSearch {
	return &hostedtool.WebSearch{
		AdditionalProperties: map[string]any{
			"user_location": map[string]string{
				"city":     city,
				"region":   region,
				"country":  country,
				"timezone": timezone,
			},
			"search_context_size": searchContextSize,
		},
	}
}

var _ chatclient.Client = (*client)(nil)
var _ chatclient.StructuredResponseClient = (*client)(nil)

type client struct {
	client openai.Client
	config ClientConfig
}

// ClientConfig contains configuration for [Agent].
type ClientConfig struct {
	Model    string
	APIKey   string // Optional, if not set will use default environment variable
	Endpoint string // Optional, defaults to OpenAI API

	// Only used for Azure OpenAI
	APIVersion string // Optional, defaults to latest API version
}

func newChatAgent(isAzure bool, config ClientConfig, options *chatagent.Options) *chatagent.Agent {
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
	c := &client{
		client: openai.NewClient(ops...),
		config: config,
	}
	return chatagent.NewAgent(c, options)
}

func NewChatAgent(config ClientConfig, options *chatagent.Options) *chatagent.Agent {
	return newChatAgent(false, config, options)
}

// NewChatAgentAzure creates a new [Agent].
func NewChatAgentAzure(config ClientConfig, options *chatagent.Options) *chatagent.Agent {
	return newChatAgent(true, config, options)
}

func (a *client) StructuredResponse(v any, ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
	// The OpenAI models that support structured outputs use JSON Schema for defining the response format.
	format, err := jsonformat.ForType(reflect.TypeOf(v))
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &chatclient.ChatOptions{}
	} else {
		opts = opts.Clone()
	}
	opts.ResponseFormat = format
	resp, err := a.Response(ctx, opts, messages...)
	if err != nil {
		return nil, err
	}
	if txt := resp.String(); txt != "" {
		if err := jsonformat.Unmarshal(format, []byte(txt), v); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func (a *client) Response(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
	body, err := a.buildCompletionParams(opts, messages...)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Chat.Completions.New(ctx, body)
	if err != nil {
		return nil, err
	}
	choice := resp.Choices[0]
	contents := make([]message.Content, 0, 1+len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		contents = append(contents, &message.FunctionCallContent{
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	if choice.Message.Content != "" {
		contents = append(contents, &message.TextContent{Text: choice.Message.Content})
	}
	if choice.Message.Refusal != "" {
		contents = append(contents, &message.ErrorContent{Message: choice.Message.Refusal})
	}
	return &chatclient.ChatResponse{
		Messages:     []*message.Message{{Role: message.Role(choice.Message.Role), Contents: contents}},
		ID:           resp.ID,
		FinishReason: choice.FinishReason,
	}, nil
}

func (a *client) StreamingResponse(ctx context.Context, options *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
	return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
		body, err := a.buildCompletionParams(options, messages...)
		if err != nil {
			yield(nil, err)
			return
		}
		stream := a.client.Chat.Completions.NewStreaming(ctx, body)
		defer stream.Close()
		var acc openai.ChatCompletionAccumulator
		for stream.Next() {
			chunk := stream.Current()
			if !acc.AddChunk(chunk) {
				continue
			}
			var contents []message.Content
			if tc, ok := acc.JustFinishedToolCall(); ok {
				args, err := unmarshalArgs(tc.Arguments)
				if err != nil {
					yield(nil, err)
					return
				}
				contents = append(contents, &message.FunctionCallContent{
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: args,
				})
			}
			if len(chunk.Choices) > 0 {
				if choice := chunk.Choices[0]; choice.Delta.Content != "" {
					contents = append(contents, &message.TextContent{Text: choice.Delta.Content})
				}
			}
			resp := &chatclient.ChatResponseUpdate{
				Contents:   contents,
				Role:       message.RoleAssistant,
				ResponseID: chunk.ID,
				MessageID:  chunk.ID,
				ModelID:    chunk.Model,
				CreatedAt:  time.Unix(chunk.Created, 0),
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
func (a *client) buildCompletionParams(options *chatclient.ChatOptions, messages ...*message.Message) (openai.ChatCompletionNewParams, error) {
	params := openai.ChatCompletionNewParams{
		Model:    a.config.Model,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1),
	}
	if options != nil {
		if options.Instructions != "" {
			params.Messages = append(params.Messages, openai.SystemMessage([]openai.ChatCompletionContentPartTextParam{
				{Text: options.Instructions},
			}))
		}
		if options.Temperature.Valid() {
			params.Temperature = openai.Float(options.Temperature.MustValue())
		}
		if options.TopP.Valid() {
			params.TopP = openai.Float(options.TopP.MustValue())
		}
		if options.MaxTokens.Valid() {
			params.MaxTokens = openai.Int(int64(options.MaxTokens.MustValue()))
		}
		if options.ResponseFormat != nil {
			switch options.ResponseFormat.Kind() {
			case "json":
				if schema, ok := options.ResponseFormat.(format.SchemaFormat); ok {
					params.ResponseFormat.OfJSONSchema = &shared.ResponseFormatJSONSchemaParam{
						JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
							Name:   schema.Name(),
							Schema: schema.Schema(),
						},
					}
					if desc := schema.Description(); desc != "" {
						params.ResponseFormat.OfJSONSchema.JSONSchema.Description = openai.String(desc)
					}
					if schema.Strict() {
						params.ResponseFormat.OfJSONSchema.JSONSchema.Strict = openai.Bool(true)
					}
				} else {
					// Fallback to generic JSON object format
					param := shared.NewResponseFormatJSONObjectParam()
					params.ResponseFormat.OfJSONObject = &param
				}
			}
		}
		for _, tl := range options.Tools {
			switch tl := tl.(type) {
			case *hostedtool.WebSearch:
				if location, ok := tl.AdditionalProperties["user_location"]; ok {
					if location, ok := location.(map[string]string); ok {
						if city, ok := location["city"]; ok && city != "" {
							params.WebSearchOptions.UserLocation.Approximate.City = openai.String(city)
						}
						if region, ok := location["region"]; ok && region != "" {
							params.WebSearchOptions.UserLocation.Approximate.Region = openai.String(region)
						}
						if country, ok := location["country"]; ok && country != "" {
							params.WebSearchOptions.UserLocation.Approximate.Country = openai.String(country)
						}
						if timezone, ok := location["timezone"]; ok && timezone != "" {
							params.WebSearchOptions.UserLocation.Approximate.Timezone = openai.String(timezone)
						}
					}
				}
				if contextSize, ok := tl.AdditionalProperties["search_context_size"]; ok {
					if contextSize, ok := contextSize.(string); ok && contextSize != "" {
						params.WebSearchOptions.SearchContextSize = contextSize
					}
				}
			case tool.FuncTool:
				name, description := tl.Name(), tl.Description()
				var funcParams map[string]any
				switch schema := tl.Schema().(type) {
				case map[string]any:
					funcParams = schema
				default:
					if schema == nil {
						break
					}
					data, err := json.Marshal(schema)
					if err == nil {
						err = json.Unmarshal(data, &funcParams)
					}
					if err != nil {
						return openai.ChatCompletionNewParams{}, fmt.Errorf("failed to convert function tool schema to JSON format: %w", err)
					}
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
	for _, msg := range messages {
		omsg, err := buildMessageParam(msg)
		if err != nil {
			return openai.ChatCompletionNewParams{}, err
		}
		params.Messages = append(params.Messages, omsg...)
	}
	return params, nil
}

// buildMessageParam converts an agent.Message to one or more OpenAI message parameters.
// Returns a slice because some agent messages (like RoleTool) need to be split into multiple OpenAI messages.
func buildMessageParam(msg *message.Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	switch msg.Role {
	case message.RoleSystem:
		var contents []openai.ChatCompletionContentPartTextParam
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok {
				contents = append(contents, openai.ChatCompletionContentPartTextParam{
					Text: tc.Text,
				})
			}
		}
		return []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(contents)}, nil

	case message.RoleUser:
		var contents []openai.ChatCompletionContentPartUnionParam
		for _, c := range msg.Contents {
			switch c := c.(type) {
			case *message.TextContent:
				contents = append(contents, openai.TextContentPart(c.Text))
			case *message.URIContent:
				switch c.TopLevelMediaType() {
				case "image":
					contents = append(contents, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL: c.URI,
					}))
				default:
					// For other URI content types, just ignore, they are not supported yet.
				}
			case *message.DataContent:
				switch c.TopLevelMediaType() {
				case "image":
					contents = append(contents, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL: c.Data,
					}))
				case "audio":
					var format string
					switch c.MediaType {
					case "audio/wav":
						format = "wav"
					case "audio/mp3", "audio/mpeg":
						format = "mp3"
					default:
						// Default to mp3
						format = "mp3"
					}
					contents = append(contents, openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
						Data:   c.Data,
						Format: format,
					}))
				default:
					contents = append(contents, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
						FileData: openai.String(c.Data),
						Filename: openai.String(c.Name),
					}))
				}
			case *message.HostedFileContent:
				contents = append(contents, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
					FileID: openai.String(c.FileID),
				}))
			}
		}
		return []openai.ChatCompletionMessageParamUnion{openai.UserMessage(contents)}, nil

	case message.RoleAssistant:
		var contents []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion
		var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
		for _, c := range msg.Contents {
			switch c := c.(type) {
			case *message.TextContent:
				contents = append(contents, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
					OfText: &openai.ChatCompletionContentPartTextParam{
						Text: c.Text,
					},
				})
			case *message.FunctionCallContent:
				args, err := marshalArgs(c.Arguments)
				if err != nil {
					return nil, err
				}
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: c.CallID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      c.Name,
							Arguments: args,
						},
					},
				})
			case *message.ErrorContent:
				contents = append(contents, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
					OfText: &openai.ChatCompletionContentPartTextParam{
						Text: c.Message,
					},
				})
			}
		}
		return []openai.ChatCompletionMessageParamUnion{{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content:   openai.ChatCompletionAssistantMessageParamContentUnion{OfArrayOfContentParts: contents},
				ToolCalls: toolCalls,
			},
		}}, nil

	case message.RoleTool:
		// Each tool result needs its own separate message for OpenAI API compliance
		var messages []openai.ChatCompletionMessageParamUnion
		for _, c := range msg.Contents {
			if funcResult, ok := c.(*message.FunctionResultContent); ok {
				ret := funcResult.Result
				if funcResult.Error != nil {
					ret = funcResult.Error
				} else if b, ok := ret.(json.RawMessage); ok {
					ret = string(b)
				}
				messages = append(messages, openai.ToolMessage(
					[]openai.ChatCompletionContentPartTextParam{{Text: fmt.Sprintf("%v", ret)}},
					funcResult.CallID,
				))
			}
		}
		return messages, nil

	default:
		panic("unknown message role: " + string(msg.Role))
	}
}

func marshalArgs(args any) (string, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalArgs(data string) (any, error) {
	var args any
	if err := json.Unmarshal([]byte(data), &args); err != nil {
		return nil, fmt.Errorf("can't unmarshal response args: %w", err)
	}
	return args, nil
}
