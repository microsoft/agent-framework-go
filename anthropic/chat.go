// Copyright (c) Microsoft. All rights reserved.

package anthropic

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"slices"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/chatagent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

type client struct {
	client anthropic.Client
	config ClientConfig
}

// ClientConfig contains configuration for [Agent].
type ClientConfig struct {
	Model   string
	APIKey  string // Optional, if not set will use default environment variable
	BaseURL string // Optional, defaults to Anthropic API
}

func NewChatAgent(config ClientConfig, options chatagent.Options) *chatagent.Agent {
	opts := []option.RequestOption{}
	if config.APIKey != "" {
		opts = append(opts, option.WithAPIKey(config.APIKey))
	}
	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}
	c := &client{
		client: anthropic.NewClient(opts...),
		config: config,
	}
	return chatagent.NewAgent(c.run, options)
}

func (a *client) run(ctx context.Context, messages []*message.Message, options ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	params, err := a.buildMessageParams(messages, options)
	if err != nil {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	if stream, _ := agentopt.Get(options, agentopt.Stream); !stream {
		resp, err := a.client.Messages.New(ctx, params)
		if err != nil {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, err)
			}
		}
		functions := make(map[int]*message.FunctionCallContent)
		contents := make([]message.Content, 0, len(resp.Content))
		for i, c := range resp.Content {
			contents = a.buildBlock(i, c.AsAny(), contents, functions)
		}
		indices := slices.Collect(maps.Keys(functions))
		slices.Sort(indices)
		for _, f := range indices {
			contents = append(contents, functions[f])
		}
		contents = append(contents, &message.UsageContent{
			Details: toUsageDetails(resp.Usage),
		})
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Contents:          contents,
				Role:              message.RoleAssistant,
				MessageID:         resp.ID,
				ResponseID:        resp.ID,
				CreatedAt:         time.Now(),
				RawRepresentation: resp,
			}, nil)
		}
	}
	return func(yield func(*message.ResponseUpdate, error) bool) {
		stream := a.client.Messages.NewStreaming(ctx, params)

		var messageID string
		var modelID string
		var usage message.UsageDetails
		var functions = make(map[int]*message.FunctionCallContent)

		for stream.Next() {
			event := stream.Current()

			var contents []message.Content
			switch event := event.AsAny().(type) {
			case anthropic.MessageStartEvent:
				messageID = cmp.Or(messageID, event.Message.ID)
				modelID = cmp.Or(modelID, string(event.Message.Model))
				usage.Add(toUsageDetails(event.Message.Usage))
			case anthropic.MessageDeltaEvent:
				usage.Add(toUsageDetailsDelta(event.Usage))
			case anthropic.ContentBlockStartEvent:
				contents = a.buildBlock(int(event.Index), event.ContentBlock.AsAny(), contents, functions)
			case anthropic.ContentBlockDeltaEvent:
				contents = a.buildDelta(int(event.Index), event.Delta.AsAny(), contents, functions)
			case anthropic.ContentBlockStopEvent:
				indices := slices.Collect(maps.Keys(functions))
				slices.Sort(indices)
				for _, id := range indices {
					contents = append(contents, functions[id])
				}
			}

			if !yield(&message.ResponseUpdate{
				Contents:          contents,
				Role:              message.RoleAssistant,
				ResponseID:        messageID,
				MessageID:         messageID,
				CreatedAt:         time.Now(),
				RawRepresentation: event,
			}, nil) {
				return
			}
		}
		if !yield(&message.ResponseUpdate{
			CreatedAt: time.Now(),
			Role:      message.RoleAssistant,
			MessageID: messageID,
			Contents: []message.Content{
				&message.UsageContent{
					Details: usage,
				},
			},
		}, nil) {
			return
		}
		if err := stream.Err(); err != nil {
			yield(nil, err)
		}
	}
}

func toUsageDetails(usage anthropic.Usage) message.UsageDetails {
	details := message.UsageDetails{
		InputTokenCount:       usage.InputTokens,
		OutputTokenCount:      usage.OutputTokens,
		TotalTokenCount:       usage.InputTokens + usage.OutputTokens,
		CachedInputTokenCount: usage.CacheReadInputTokens,
	}
	if usage.CacheCreationInputTokens != 0 {
		if details.AdditionalCounts == nil {
			details.AdditionalCounts = make(map[string]int64)
		}
		details.AdditionalCounts["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
	return details
}

func toUsageDetailsDelta(usage anthropic.MessageDeltaUsage) message.UsageDetails {
	return toUsageDetails(anthropic.Usage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
	})
}

func (a *client) buildBlock(index int, v any, contents []message.Content, functions map[int]*message.FunctionCallContent) []message.Content {
	switch v := v.(type) {
	case anthropic.TextBlock:
		contents = append(contents, &message.TextContent{
			Text: v.Text,
			ContentHeader: message.ContentHeader{
				RawRepresentation: v,
			},
		})
	case anthropic.ThinkingBlock:
		contents = append(contents, &message.TextReasoningContent{
			ProtectedData: v.Signature,
			Text:          v.Thinking,
			ContentHeader: message.ContentHeader{
				RawRepresentation: v,
			},
		})
	case anthropic.RedactedThinkingBlock:
		contents = append(contents, &message.TextReasoningContent{
			ProtectedData: v.Data,
			ContentHeader: message.ContentHeader{
				RawRepresentation: v,
			},
		})
	case anthropic.ToolUseBlock:
		functions[index] = &message.FunctionCallContent{
			CallID:    v.ID,
			Name:      v.Name,
			Arguments: string(v.Input),
		}
	}
	return contents
}

func (a *client) buildDelta(index int, v any, contents []message.Content, functions map[int]*message.FunctionCallContent) []message.Content {
	switch d := v.(type) {
	case anthropic.TextDelta:
		contents = append(contents, &message.TextContent{
			Text: d.Text,
			ContentHeader: message.ContentHeader{
				RawRepresentation: d,
			},
		})
	case anthropic.InputJSONDelta:
		if fnContent, ok := functions[index]; ok {
			fnContent.Arguments += d.PartialJSON
		}
	case anthropic.ThinkingDelta:
		contents = append(contents, &message.TextReasoningContent{
			Text: d.Thinking,
			ContentHeader: message.ContentHeader{
				RawRepresentation: d,
			},
		})
	case anthropic.SignatureDelta:
		contents = append(contents, &message.TextReasoningContent{
			ProtectedData: d.Signature,
			ContentHeader: message.ContentHeader{
				RawRepresentation: d,
			},
		})
	}
	return contents
}

func (a *client) buildMessageParams(messages []*message.Message, opts []agentopt.RunOption) (anthropic.MessageNewParams, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.config.Model),
		Messages:  []anthropic.MessageParam{},
		MaxTokens: 1024, // Default max tokens
	}

	if temperature, ok := agentopt.Get(opts, chatagent.Temperature); ok {
		params.Temperature = anthropic.Float(temperature)
	}
	if topP, ok := agentopt.Get(opts, chatagent.TopP); ok {
		params.TopP = anthropic.Float(topP)
	}
	if maxTokens, ok := agentopt.Get(opts, chatagent.MaxOutputTokens); ok {
		params.MaxTokens = maxTokens
	}

	var tools []anthropic.ToolUnionParam
	for tl := range agentopt.All(opts, agentopt.Tool) {
		if ft, ok := tl.(tool.FuncTool); ok {
			name, description := ft.Name(), ft.Description()
			var properties any
			var required []string

			// Extract schema details - first convert to map[string]any if needed
			schema := ft.Schema()
			var schemaMap map[string]any

			switch s := schema.(type) {
			case map[string]any:
				schemaMap = s
			default:
				// For *jsonschema.Schema or other types, marshal to JSON then unmarshal to map
				if schema != nil {
					jsonBytes, err := json.Marshal(schema)
					if err == nil {
						json.Unmarshal(jsonBytes, &schemaMap)
					}
				}
			}

			if schemaMap != nil {
				if props, ok := schemaMap["properties"]; ok {
					properties = props
				}
				if reqs, ok := schemaMap["required"]; ok {
					if reqList, ok := reqs.([]any); ok {
						for _, r := range reqList {
							if s, ok := r.(string); ok {
								required = append(required, s)
							}
						}
					} else if reqList, ok := reqs.([]string); ok {
						required = reqList
					}
				}
			}

			schemaParam := anthropic.ToolInputSchemaParam{
				Properties: properties,
				Required:   required,
			}

			toolParam := anthropic.ToolUnionParamOfTool(schemaParam, name)
			if toolParam.OfTool != nil {
				toolParam.OfTool.Description = anthropic.String(description)
			}
			tools = append(tools, toolParam)
		}
	}
	if len(tools) > 0 {
		params.Tools = tools
	}

	if mode, ok := agentopt.Get(opts, agentopt.ToolMode); ok {
		switch mode {
		case tool.ToolModeAuto:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		case tool.ToolModeRequired:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{},
			}
		default:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{
					Name: string(mode),
				},
			}
		}
	}

	for _, msg := range messages {
		switch msg.Role {
		case message.RoleUser, message.RoleAssistant, message.RoleTool:
			amsg, err := buildMessageParam(msg)
			if err != nil {
				return anthropic.MessageNewParams{}, err
			}
			params.Messages = append(params.Messages, amsg)
		case message.RoleSystem:
			for _, c := range msg.Contents {
				if c, ok := c.(*message.TextContent); ok {
					if c.Text != "" {
						params.System = append(params.System, anthropic.TextBlockParam{
							Text: c.Text,
						})
					}
				}
			}
		default:
			// Ignore
		}
	}

	return params, nil
}

func buildMessageParam(msg *message.Message) (anthropic.MessageParam, error) {
	var content []anthropic.ContentBlockParamUnion

	for _, c := range msg.Contents {
		switch c := c.(type) {
		case *message.TextContent:
			content = append(content, anthropic.NewTextBlock(c.Text))
		case *message.FunctionCallContent:
			// Parse the JSON string arguments into a map for Anthropic SDK
			var args map[string]any
			if c.Arguments != "" {
				if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
					return anthropic.MessageParam{}, fmt.Errorf("failed to unmarshal tool arguments: %w", err)
				}
			}
			content = append(content, anthropic.NewToolUseBlock(c.CallID, args, c.Name))
		case *message.FunctionResultContent:
			resStr := ""
			switch r := c.Result.(type) {
			case json.RawMessage:
				resStr = string(r)
			case string:
				resStr = r
			case []byte:
				resStr = string(r)
			default:
				// Marshal any other type to JSON for proper formatting
				jsonBytes, err := json.Marshal(c.Result)
				if err != nil {
					resStr = fmt.Sprintf("%v", c.Result)
				} else {
					resStr = string(jsonBytes)
				}
			}
			content = append(content, anthropic.NewToolResultBlock(c.CallID, resStr, c.Error != nil))
		case *message.DataContent:
			if c.TopLevelMediaType() == "image" {
				mediaType := c.MediaType
				if mediaType == "" {
					mediaType = "image/jpeg"
				}
				content = append(content, anthropic.NewImageBlockBase64(mediaType, string(c.Data)))
			}
		}
	}

	switch msg.Role {
	case message.RoleAssistant:
		return anthropic.NewAssistantMessage(content...), nil
	case message.RoleTool:
		// Tool results are user messages in Anthropic
		return anthropic.NewUserMessage(content...), nil
	}

	return anthropic.NewUserMessage(content...), nil
}
