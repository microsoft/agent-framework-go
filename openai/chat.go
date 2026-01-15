// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"reflect"
	"time"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
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

type client struct {
	client openai.Client
	config ClientConfig
}

// ClientConfig contains configuration for [Agent].
type ClientConfig struct {
	Model      string // required
	APIKey     string
	Endpoint   string
	HttpClient *http.Client

	// Only used for Azure OpenAI
	APIVersion string
}

func newChatAgent(isAzure bool, config ClientConfig, options chatagent.Config) *chatagent.Agent {
	ops := make([]option.RequestOption, 0, 2)
	if isAzure {
		if config.Endpoint != "" {
			// The latest API versions, including previews, can be found here:
			// https://learn.microsoft.com/en-us/azure/ai-services/openai/reference#rest-api-versioning
			apiVersion := cmp.Or(config.APIVersion, "2025-01-01-preview")
			ops = append(ops, azure.WithEndpoint(config.Endpoint, apiVersion))
		}
	} else {
		if config.Endpoint != "" {
			ops = append(ops, option.WithBaseURL(config.Endpoint))
		}
	}
	if config.APIKey != "" {
		if isAzure {
			ops = append(ops, azure.WithAPIKey(config.APIKey))
		} else {
			ops = append(ops, option.WithAPIKey(config.APIKey))
		}
	}
	if config.HttpClient != nil {
		ops = append(ops, option.WithHTTPClient(config.HttpClient))
	}
	c := &client{
		client: openai.NewClient(ops...),
		config: config,
	}
	if options.FormatOfFn == nil {
		options.FormatOfFn = c.formatOf
	}
	if options.UnmarshalFn == nil {
		options.UnmarshalFn = c.unmarshal
	}
	return chatagent.NewAgent(c.run, options)
}

func NewChatAgent(config ClientConfig, options chatagent.Config) *chatagent.Agent {
	return newChatAgent(false, config, options)
}

// NewChatAgentAzure creates a new [Agent].
func NewChatAgentAzure(config ClientConfig, options chatagent.Config) *chatagent.Agent {
	return newChatAgent(true, config, options)
}

func (a *client) formatOf(v any) (format.Format, error) {
	return jsonformat.ForType(reflect.TypeOf(v))
}

func (a *client) unmarshal(format format.Format, data []byte, v any) error {
	return jsonformat.Unmarshal(format.(*jsonformat.Format), data, v)
}

func (a *client) run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	body, err := a.buildCompletionParams(messages, options)
	if err != nil {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	if stream, _ := agentopt.Get(options, agentopt.Stream); !stream {
		resp, err := a.client.Chat.Completions.New(ctx, body)
		if err != nil {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, err)
			}
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
		if resp.JSON.Usage.Valid() {
			contents = addUsage(contents, resp.Usage)
		}
		return func(yield func(*message.ResponseUpdate, error) bool) {
			update := &message.ResponseUpdate{
				Contents:   contents,
				Role:       message.RoleAssistant,
				ResponseID: resp.ID,
				MessageID:  resp.ID,
				CreatedAt:  time.Unix(resp.Created, 0),
			}
			if !yield(update, nil) {
				return
			}
		}
	}
	return func(yield func(*message.ResponseUpdate, error) bool) {
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
				contents = append(contents, &message.FunctionCallContent{
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				})
			}
			role := message.RoleAssistant
			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]
				if choice.Delta.Content != "" {
					contents = append(contents, &message.TextContent{Text: choice.Delta.Content})
				}
				role = mapRole(choice.Delta.Role)
			}
			if chunk.JSON.Usage.Valid() {
				contents = addUsage(contents, chunk.Usage)
			}
			resp := &message.ResponseUpdate{
				Contents:   contents,
				Role:       role,
				ResponseID: chunk.ID,
				MessageID:  chunk.ID,
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

func mapRole(r string) message.Role {
	switch r {
	case "user":
		return message.RoleUser
	case "system":
		return message.RoleSystem
	case "tool":
		return message.RoleTool
	case "assistant", "developer":
		return message.RoleAssistant
	default:
		return message.RoleAssistant
	}
}

// buildCompletionParams constructs the parameters for the OpenAI chat completion API.
func (a *client) buildCompletionParams(messages []*message.Message, opts []agentopt.RunOption) (openai.ChatCompletionNewParams, error) {
	params := openai.ChatCompletionNewParams{
		Model:    a.config.Model,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1),
	}
	if v, ok := agentopt.Get(opts, chatagent.Model); ok && v != "" {
		params.Model = v
	}
	if v, ok := agentopt.Get(opts, chatagent.Temperature); ok {
		params.Temperature = openai.Float(v)
	}
	if v, ok := agentopt.Get(opts, chatagent.TopP); ok {
		params.TopP = openai.Float(v)
	}
	if v, ok := agentopt.Get(opts, chatagent.MaxOutputTokens); ok {
		params.MaxCompletionTokens = openai.Int(v)
	}
	if v, ok := agentopt.Get(opts, chatagent.PresencePenalty); ok {
		params.PresencePenalty = openai.Float(v)
	}
	if v, ok := agentopt.Get(opts, chatagent.FrequencyPenalty); ok {
		params.FrequencyPenalty = openai.Float(v)
	}
	if v, ok := agentopt.Get(opts, chatagent.Seed); ok {
		params.Seed = openai.Int(v)
	}
	if v, ok := agentopt.Get(opts, chatagent.StopSequences); ok && len(v) > 0 {
		params.Stop.OfStringArray = v
	}
	if frmt, ok := agentopt.Get(opts, agentopt.ResponseFormat); ok && frmt != nil {
		switch frmt.Kind() {
		case "json":
			if schema, ok := frmt.(format.SchemaFormat); ok {
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
	first := true
	for tl := range agentopt.All(opts, agentopt.Tool) {
		if first {
			first = false
			if v, ok := agentopt.Get(opts, chatagent.AllowMultipleToolCalls); ok {
				params.ParallelToolCalls = openai.Bool(v)
			}
			if mode, ok := agentopt.Get(opts, agentopt.ToolMode); ok {
				params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
					OfAuto: openai.String(string(mode)),
				}
			}
		}
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
		if len(contents) == 0 {
			return nil, nil
		}
		if len(contents) == 1 {
			return []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(contents[0].Text)}, nil
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
					contents = append(contents, openai.ChatCompletionContentPartUnionParam{
						OfImageURL: &openai.ChatCompletionContentPartImageParam{
							ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
								URL:    c.URI,
								Detail: imageDetail(c.AdditionalProperties),
							},
						},
					})
				default:
					// For other URI content types, just ignore, they are not supported yet.
				}
			case *message.DataContent:
				switch c.TopLevelMediaType() {
				case "image":
					contents = append(contents, openai.ChatCompletionContentPartUnionParam{
						OfImageURL: &openai.ChatCompletionContentPartImageParam{
							ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
								URL:    c.URI(),
								Detail: imageDetail(c.AdditionalProperties),
							},
						},
					})
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
		if len(contents) == 0 {
			return nil, nil
		}
		if len(contents) == 1 && contents[0].OfText != nil {
			return []openai.ChatCompletionMessageParamUnion{openai.UserMessage(contents[0].OfText.Text)}, nil
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
		if len(contents) == 0 && len(toolCalls) == 0 {
			return nil, nil
		}
		var content openai.ChatCompletionAssistantMessageParamContentUnion
		if len(contents) == 1 && contents[0].OfText != nil {
			content = openai.ChatCompletionAssistantMessageParamContentUnion{OfString: openai.String(contents[0].OfText.Text)}
		} else {
			content = openai.ChatCompletionAssistantMessageParamContentUnion{OfArrayOfContentParts: contents}
		}
		return []openai.ChatCompletionMessageParamUnion{{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content:   content,
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
				messages = append(messages, openai.ToolMessage(fmt.Sprintf("%v", ret), funcResult.CallID))
			}
		}
		return messages, nil

	default:
		panic("unknown message role: " + string(msg.Role))
	}
}

func addUsage(contents []message.Content, usage openai.CompletionUsage) []message.Content {
	details := message.UsageDetails{
		InputTokenCount:       usage.PromptTokens,
		OutputTokenCount:      usage.CompletionTokens,
		TotalTokenCount:       usage.TotalTokens,
		CachedInputTokenCount: usage.PromptTokensDetails.CachedTokens,
		ReasoningTokenCount:   usage.CompletionTokensDetails.ReasoningTokens,
		AdditionalCounts:      make(map[string]int64),
	}
	details.AdditionalCounts["PromptTokensDetails.AudioTokens"] = usage.PromptTokensDetails.AudioTokens
	details.AdditionalCounts["CompletionTokensDetails.AudioTokens"] = usage.CompletionTokensDetails.AudioTokens
	details.AdditionalCounts["CompletionTokensDetails.AcceptedPredictionTokens"] = usage.CompletionTokensDetails.AcceptedPredictionTokens
	details.AdditionalCounts["CompletionTokensDetails.RejectedPredictionTokens"] = usage.CompletionTokensDetails.RejectedPredictionTokens
	return append(contents, &message.UsageContent{Details: details})
}

func imageDetail(props map[string]any) string {
	if detail, ok := props["detail"]; ok {
		if v, ok := detail.(string); ok {
			return v
		}
	}
	return ""
}
