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
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/agent/chatagent/chatclient"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/tool"
)

var _ chatclient.Client = (*client)(nil)

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

func NewChatAgent(config ClientConfig, options *chatagent.Options) *chatagent.Agent {
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
	return chatagent.NewAgent(c, options)
}

func (a *client) Capabilities() chatclient.Capabilities {
	return chatclient.Capabilities{
		Streaming: true,
	}
}

func (a *client) Response(ctx context.Context, opts chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
	params, err := a.buildMessageParams(&opts, messages...)
	if err != nil {
		return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	if !opts.Streaming.Or(false) {
		resp, err := a.client.Messages.New(ctx, params)
		if err != nil {
			return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
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
		return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
			yield(&chatclient.ChatResponseUpdate{
				Contents:          contents,
				Role:              message.RoleAssistant,
				MessageID:         resp.ID,
				ResponseID:        resp.ID,
				ModelID:           string(resp.Model),
				FinishReason:      string(resp.StopReason),
				CreatedAt:         time.Now(),
				RawRepresentation: resp,
			}, nil)
		}
	}
	return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
		stream := a.client.Messages.NewStreaming(ctx, params)

		var messageID string
		var modelID string
		var usage message.UsageDetails
		var finishReason string
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
				finishReason = string(event.Delta.StopReason)
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

			if !yield(&chatclient.ChatResponseUpdate{
				Contents:          contents,
				Role:              message.RoleAssistant,
				ResponseID:        messageID,
				MessageID:         messageID,
				ModelID:           modelID,
				FinishReason:      finishReason,
				CreatedAt:         time.Now(),
				RawRepresentation: event,
			}, nil) {
				return
			}
		}
		if !yield(&chatclient.ChatResponseUpdate{
			CreatedAt:    time.Now(),
			FinishReason: finishReason,
			Role:         message.RoleAssistant,
			MessageID:    messageID,
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
		InputTokenCount:  usage.InputTokens,
		OutputTokenCount: usage.OutputTokens,
		TotalTokenCount:  usage.InputTokens + usage.OutputTokens,
	}
	if usage.CacheCreationInputTokens != 0 {
		if details.AdditionalCounts == nil {
			details.AdditionalCounts = make(map[string]int64)
		}
		details.AdditionalCounts["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
	if usage.CacheReadInputTokens != 0 {
		if details.AdditionalCounts == nil {
			details.AdditionalCounts = make(map[string]int64)
		}
		details.AdditionalCounts["cache_read_input_tokens"] = usage.CacheReadInputTokens
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

func (a *client) buildMessageParams(options *chatclient.ChatOptions, messages ...*message.Message) (anthropic.MessageNewParams, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.config.Model),
		Messages:  []anthropic.MessageParam{},
		MaxTokens: 1024, // Default max tokens
	}

	if options.Instructions != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: options.Instructions, Type: constant.Text("text")},
		}
	}
	if options.Temperature.Valid() {
		params.Temperature = anthropic.Float(options.Temperature.MustValue())
	}
	if options.TopP.Valid() {
		params.TopP = anthropic.Float(options.TopP.MustValue())
	}
	if options.MaxTokens.Valid() {
		params.MaxTokens = int64(options.MaxTokens.MustValue())
	}

	var tools []anthropic.ToolUnionParam
	for _, tl := range options.Tools {
		if ft, ok := tl.(tool.FuncTool); ok {
			name, description := ft.Name(), ft.Description()
			var properties any
			var required []string

			// Extract schema details
			switch schema := ft.Schema().(type) {
			case map[string]any:
				if props, ok := schema["properties"]; ok {
					properties = props
				}
				if reqs, ok := schema["required"]; ok {
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
			default:
				// Fallback or error handling
			}

			schemaParam := anthropic.ToolInputSchemaParam{
				Type:       constant.Object("object"),
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

	if options.ToolMode != "" {
		switch options.ToolMode {
		case tool.ToolModeAuto:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{Type: constant.Auto("auto")},
			}
		case tool.ToolModeRequired:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAny: &anthropic.ToolChoiceAnyParam{Type: constant.Any("any")},
			}
		default:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{
					Type: constant.Tool("tool"),
					Name: string(options.ToolMode),
				},
			}
		}
	}

	var msgs []anthropic.MessageParam
	for _, msg := range messages {
		amsg, err := buildMessageParam(msg)
		if err != nil {
			return anthropic.MessageNewParams{}, err
		}
		if msg.Role == message.RoleSystem {
			continue
		}
		msgs = append(msgs, amsg)
	}
	params.Messages = msgs

	return params, nil
}

func buildMessageParam(msg *message.Message) (anthropic.MessageParam, error) {
	var content []anthropic.ContentBlockParamUnion

	for _, c := range msg.Contents {
		switch c := c.(type) {
		case *message.TextContent:
			content = append(content, anthropic.NewTextBlock(c.Text))
		case *message.FunctionCallContent:
			content = append(content, anthropic.NewToolUseBlock(c.CallID, c.Arguments, c.Name))
		case *message.FunctionResultContent:
			resStr := ""
			if b, ok := c.Result.(json.RawMessage); ok {
				resStr = string(b)
			} else {
				resStr = fmt.Sprintf("%v", c.Result)
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
