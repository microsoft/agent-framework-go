// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"cmp"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/format"
	"github.com/microsoft/agent-framework/go/format/jsonformat"
	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
	"github.com/microsoft/agent-framework/go/tool/websearchtool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

type client struct {
	client openai.Client
	config AgentConfig
}

// AgentConfig contains configuration for [Agent].
type AgentConfig struct {
	ID                 string
	Name               string
	SystemInstructions string

	Model    string
	APIKey   string // Optional, if not set will use default environment variable
	Endpoint string // Optional, defaults to OpenAI API

	// Only used for Azure OpenAI
	APIVersion string // Optional, defaults to latest API version

	NewContextProvider func() memory.ContextProvider

	Opts *agent.RunOptions
}

func newChatAgent(isAzure bool, config AgentConfig) *agent.Agent {
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
	if config.ID == "" {
		config.ID = uuid.New().String()
	}
	c := &client{
		client: openai.NewClient(ops...),
		config: config,
	}
	return &agent.Agent{
		Config: agent.Config{
			ID:                 config.ID,
			Name:               config.Name,
			Opts:               config.Opts,
			SystemInstructions: config.SystemInstructions,
			Run:                c.Run,
			RunStream:          c.RunStream,
			RunOf:              c.RunOf,
			NewContextProvider: config.NewContextProvider,
		},
	}
}

func NewChatAgent(config AgentConfig) *agent.Agent {
	return newChatAgent(false, config)
}

// NewChatAgentAzure creates a new [Agent].
func NewChatAgentAzure(config AgentConfig) *agent.Agent {
	return newChatAgent(true, config)
}

func (a *client) RunOf(v any, ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
	// The OpenAI models that support structured outputs use JSON Schema for defining the response format.
	format, err := jsonformat.ForType(reflect.TypeOf(v))
	if err != nil {
		return nil, err
	}
	ctx.Options.ResponseFormat = format
	resp, err := a.Run(ctx, messages...)
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

func (a *client) Run(ctx *agent.RunContext, messages ...*message.Message) (*agent.RunResponse, error) {
	resp, err := a.client.Chat.Completions.New(ctx, a.buildCompletionParams(ctx.Options, messages...))
	if err != nil {
		return nil, err
	}
	choice := resp.Choices[0]
	contents := make([]message.Content, 0, 1+len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		contents = append(contents, &message.FunctionCallContent{
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	if choice.Message.Content != "" {
		contents = append(contents, &message.TextContent{Text: choice.Message.Content})
	}
	if choice.Message.Refusal != "" {
		contents = append(contents, &message.ErrorContent{Message: choice.Message.Refusal})
	}
	return &agent.RunResponse{
		Messages:     []*message.Message{{Role: message.Role(choice.Message.Role), Contents: contents}},
		AgentID:      a.config.ID,
		ResponseID:   resp.ID,
		FinishReason: choice.FinishReason,
	}, nil
}

func (a *client) RunStream(ctx *agent.RunContext, messages ...*message.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	stream := a.client.Chat.Completions.NewStreaming(ctx, a.buildCompletionParams(ctx.Options, messages...))
	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		defer stream.Close()
		var acc openai.ChatCompletionAccumulator
		for stream.Next() {
			chunk := stream.Current()
			if !acc.AddChunk(chunk) {
				continue
			}
			var contents []message.Content
			if tc, ok := acc.JustFinishedToolCall(); ok {
				contents = append(contents, &message.FunctionCallContent{
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				})
			}
			if len(chunk.Choices) > 0 {
				if choice := chunk.Choices[0]; choice.Delta.Content != "" {
					contents = append(contents, &message.TextContent{Text: choice.Delta.Content})
				}
			}
			resp := &agent.RunResponseUpdate{
				Contents:   contents,
				AgentID:    a.config.ID,
				Role:       message.RoleAssistant,
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
func (a *client) buildCompletionParams(options *agent.RunOptions, messages ...*message.Message) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    a.config.Model,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1),
	}
	for _, msg := range messages {
		params.Messages = append(params.Messages, buildMessageParam(msg)...)
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
			case *websearchtool.HostedWebSearch:
				if location, ok := tl.AdditionalProperties["user_location"]; ok {
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
			case tool.CallTool:
				name, description := tl.ToolInfo()
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

// buildMessageParam converts an agent.Message to one or more OpenAI message parameters.
// Returns a slice because some agent messages (like RoleTool) need to be split into multiple OpenAI messages.
func buildMessageParam(msg *message.Message) []openai.ChatCompletionMessageParamUnion {
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
		return []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(contents)}

	case message.RoleUser:
		var contents []openai.ChatCompletionContentPartUnionParam
		for _, c := range msg.Contents {
			switch c := c.(type) {
			case *message.TextContent:
				contents = append(contents, openai.TextContentPart(c.Text))
			case *message.URIContent:
				switch topLevelMediaType(c.MediaType) {
				case "image":
					contents = append(contents, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL: c.URI,
					}))
				default:
					// For other URI content types, just ignore, they are not supported yet.
				}
			case *message.DataContent:
				switch topLevelMediaType(c.MediaType) {
				case "image":
					contents = append(contents, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL: c.URI,
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
						Data:   string(c.Data),
						Format: format,
					}))
				default:
					contents = append(contents, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
						FileData: openai.String(string(c.Data)),
						Filename: openai.String(c.Name),
					}))
				}
			case *message.HostedFileContent:
				contents = append(contents, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
					FileID: openai.String(c.FileID),
				}))
			}
		}
		return []openai.ChatCompletionMessageParamUnion{openai.UserMessage(contents)}

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
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: c.CallID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      c.Name,
							Arguments: c.Arguments,
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
		}}

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
		return messages

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
