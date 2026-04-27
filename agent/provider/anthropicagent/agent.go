// Copyright (c) Microsoft. All rights reserved.

package anthropicagent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

type messageNewParamsOpt anthropic.MessageNewParams

func (o messageNewParamsOpt) Value() any { return anthropic.MessageNewParams(o) }

// MessageNewParams allows passing custom parameters to the underlying anthropic API calls.
func MessageNewParams(params anthropic.MessageNewParams) agent.Option {
	return messageNewParamsOpt(params)
}

type client struct {
	client anthropic.Client
	config Config
}

// Config contains configuration for [New].
type Config struct {
	agent.Config

	Model string
}

func New(aclient anthropic.Client, config Config) *agent.Agent {
	c := &client{
		client: aclient,
		config: config,
	}
	return agent.New(agent.ProviderConfig{
		Run:          c.run,
		ProviderName: "anthropic",
		FormatOfFn:   c.formatOf,
		UnmarshalFn:  c.unmarshal,
	}, config.Config)
}

func (a *client) formatOf(v any) (format.Format, error) {
	return jsonformat.ForType(reflect.TypeOf(v))
}

func (a *client) unmarshal(f format.Format, data []byte, v any) error {
	return f.(*jsonformat.Format).Unmarshal(data, v)
}

func (a *client) run(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
	params, err := a.buildMessageParams(messages, options)
	if err != nil {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	if stream, _ := agent.GetOption(options, agent.Stream); !stream {
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
		functions := make(map[int]*message.FunctionCallContent)

		for stream.Next() {
			event := stream.Current()

			var contents []message.Content
			switch event := event.AsAny().(type) {
			case anthropic.MessageStartEvent:
				messageID = cmp.Or(messageID, event.Message.ID)
				modelID = cmp.Or(modelID, event.Message.Model)
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

func (a *client) buildMessageParams(messages []*message.Message, opts []agent.Option) (anthropic.MessageNewParams, error) {
	var params anthropic.MessageNewParams
	if p, ok := agent.GetOption(opts, MessageNewParams); ok {
		params = p
	}
	params.Model = cmp.Or(params.Model, a.config.Model)
	params.MaxTokens = cmp.Or(params.MaxTokens, 4096)

	var tools []anthropic.ToolUnionParam
	for tl := range agent.AllOptions(opts, agent.WithTool) {
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
						_ = json.Unmarshal(jsonBytes, &schemaMap)
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

	if mode, ok := agent.GetOption(opts, agent.WithToolMode); ok {
		switch mode.Mode() {
		case tool.ToolModeAuto, "":
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfAuto: &anthropic.ToolChoiceAutoParam{},
			}
		case tool.ToolModeNone:
			params.ToolChoice = anthropic.ToolChoiceUnionParam{
				OfNone: &anthropic.ToolChoiceNoneParam{},
			}
		case tool.ToolModeRequired:
			names := mode.Required()
			if len(names) != 1 {
				// Anthropic requires either a single tool name or "any" for multiple tools
				params.ToolChoice = anthropic.ToolChoiceUnionParam{
					OfAny: &anthropic.ToolChoiceAnyParam{},
				}
			} else {
				params.ToolChoice = anthropic.ToolChoiceParamOfTool(names[0])
			}
		}
	}

	if frmt, ok := agent.GetOption(opts, agent.WithResponseFormat); ok && frmt != nil {
		if frmt.Kind() == "json" {
			if schemaFmt, ok := frmt.(format.SchemaFormat); ok {
				var schemaMap map[string]any
				switch s := schemaFmt.Schema().(type) {
				case map[string]any:
					schemaMap = s
				default:
					if s != nil {
						jsonBytes, err := json.Marshal(s)
						if err != nil {
							return anthropic.MessageNewParams{}, fmt.Errorf("failed to marshal structured output schema: %w", err)
						}
						if err := json.Unmarshal(jsonBytes, &schemaMap); err != nil {
							return anthropic.MessageNewParams{}, fmt.Errorf("failed to unmarshal structured output schema: %w", err)
						}
					}
				}
				if schemaMap != nil {
					// BetaJSONSchemaOutputFormat normalizes the schema for
					// Anthropic requirements (e.g. adds additionalProperties:false
					// to every object layer). We reuse its Schema field in the
					// non-beta JSONOutputFormatParam.
					normalized := anthropic.BetaJSONSchemaOutputFormat(schemaMap)
					params.OutputConfig.Format = anthropic.JSONOutputFormatParam{
						Schema: normalized.Schema,
					}
				} else {
					// No usable schema provided; still request generic JSON output.
					params.OutputConfig.Format = anthropic.JSONOutputFormatParam{}
				}
			} else {
				// JSON format requested without a schema; request generic JSON output.
				params.OutputConfig.Format = anthropic.JSONOutputFormatParam{}
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
				content = append(content, anthropic.NewImageBlockBase64(mediaType, c.Data))
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
