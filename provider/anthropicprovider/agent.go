// Copyright (c) Microsoft. All rights reserved.

package anthropicprovider

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
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
	config AgentConfig
}

// AgentConfig contains configuration for [NewAgent].
type AgentConfig struct {
	agent.Config

	// Instructions are provided to Anthropic as system instructions for each run.
	Instructions string

	Model string
}

// NewAgent creates a new [agent.Agent] backed by the Anthropic Messages API via the anthropic client.
func NewAgent(aclient anthropic.Client, config AgentConfig) *agent.Agent {
	c := &client{
		client: aclient,
		config: config,
	}
	if config.Instructions != "" {
		config.RunOptions = append(config.RunOptions, agent.WithInstructions(config.Instructions))
	}
	var providerMiddlewares []agent.Middleware
	if !config.DisableFuncAutoCall {
		providerMiddlewares = append(providerMiddlewares, toolautocall.New(toolautocall.Config{
			Logger:           config.Logger,
			LogSensitiveData: config.LogSensitiveData,
		}))
	}
	return agent.New(agent.ProviderConfig{
		Run:          c.run,
		ProviderName: "anthropic",
		Middlewares:  providerMiddlewares,
		Format:       c.formatOf,
		Unmarshal:    c.unmarshal,
	}, config.Config)
}

func (a *client) formatOf(v any) (agent.ResponseFormat, error) {
	return jsonformat.ForType(reflect.TypeOf(v))
}

func (a *client) unmarshal(f agent.ResponseFormat, data []byte, v any) error {
	format, err := jsonformat.FromResponseFormat(f)
	if err != nil {
		return err
	}
	return format.Unmarshal(data, v)
}

func (a *client) run(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	params, err := a.buildMessageParams(messages, options)
	if err != nil {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	if stream, _ := agent.GetOption(options, agent.Stream); !stream {
		resp, err := a.client.Messages.New(ctx, params)
		if err != nil {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
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
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Contents:          contents,
				Role:              message.RoleAssistant,
				MessageID:         resp.ID,
				ResponseID:        resp.ID,
				CreatedAt:         time.Now(),
				RawRepresentation: resp,
			}, nil)
		}
	}
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		stream := a.client.Messages.NewStreaming(ctx, params)

		var messageID string
		var usage message.UsageDetails
		var accumulated anthropic.Message

		for stream.Next() {
			event := stream.Current()
			if err := accumulated.Accumulate(event); err != nil {
				yield(nil, err)
				return
			}

			var contents []message.Content
			switch event := event.AsAny().(type) {
			case anthropic.MessageStartEvent:
				messageID = cmp.Or(messageID, event.Message.ID)
				usage.Add(toUsageDetails(event.Message.Usage))
			case anthropic.MessageDeltaEvent:
				usage.Add(toUsageDetailsDelta(event.Usage))
			case anthropic.ContentBlockStartEvent:
				block := event.ContentBlock.AsAny()
				if _, isToolUse := block.(anthropic.ToolUseBlock); !isToolUse {
					contents = a.buildBlock(int(event.Index), block, contents, nil)
				}
			case anthropic.ContentBlockDeltaEvent:
				contents = a.buildDelta(event.Delta.AsAny(), contents)
			case anthropic.ContentBlockStopEvent:
				if block, ok := accumulated.Content[event.Index].AsAny().(anthropic.ToolUseBlock); ok {
					contents = append(contents, &message.FunctionCallContent{
						CallID:    block.ID,
						Name:      block.Name,
						Arguments: string(block.Input),
					})
				}
			}

			if !yield(&agent.ResponseUpdate{
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
		if !yield(&agent.ResponseUpdate{
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
		var annotations []message.Annotation
		for _, citation := range v.Citations {
			annotations = append(annotations, &message.CitationAnnotation{
				FileID:            citation.FileID,
				Snippet:           citation.CitedText,
				Title:             cmp.Or(citation.DocumentTitle, citation.Title),
				URL:               citation.URL,
				RawRepresentation: citation,
			})
		}
		contents = append(contents, &message.TextContent{
			Text: v.Text,
			ContentHeader: message.ContentHeader{
				Annotations:       annotations,
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

func (a *client) buildDelta(v any, contents []message.Content) []message.Content {
	switch d := v.(type) {
	case anthropic.TextDelta:
		contents = append(contents, &message.TextContent{
			Text: d.Text,
			ContentHeader: message.ContentHeader{
				RawRepresentation: d,
			},
		})
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
		// Clone the mutable slice fields appended to below so we never mutate
		// the caller's backing arrays (the option stores a shallow copy of the
		// struct); the gemini provider clones for the same reason.
		params.System = slices.Clone(params.System)
		params.Messages = slices.Clone(params.Messages)
	}
	params.Model = cmp.Or(params.Model, a.config.Model)
	params.MaxTokens = cmp.Or(params.MaxTokens, 4096)
	instructions := slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
	if len(instructions) > 0 {
		params.System = append(params.System, anthropic.TextBlockParam{Text: strings.Join(instructions, "\n")})
	}

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

	if frmt, ok := agent.GetOption(opts, agent.WithResponseFormat); ok {
		if frmt.Kind == "json" {
			if schema := frmt.Schema; schema != nil {
				var schemaMap map[string]any
				switch s := schema.(type) {
				case map[string]any:
					schemaMap = s
				default:
					jsonBytes, err := json.Marshal(s)
					if err != nil {
						return anthropic.MessageNewParams{}, fmt.Errorf("failed to marshal structured output schema: %w", err)
					}
					if err := json.Unmarshal(jsonBytes, &schemaMap); err != nil {
						return anthropic.MessageNewParams{}, fmt.Errorf("failed to unmarshal structured output schema: %w", err)
					}
				}
				if schemaMap != nil {
					params.OutputConfig.Format = anthropic.JSONOutputFormatParam{
						Schema: anthropic.BetaJSONSchemaOutputFormat(schemaMap).Schema.(map[string]any),
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
			if args == nil {
				// Anthropic requires a tool_use block's input to be an object; a
				// nil map serializes to null (rejected by the API), so send {}
				// for a tool call with empty or absent arguments.
				args = map[string]any{}
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
			switch {
			case c.TopLevelMediaType() == "image":
				mediaType := c.MediaType
				if mediaType == "" {
					mediaType = "image/jpeg"
				}
				content = append(content, anthropic.NewImageBlockBase64(mediaType, c.Data))
			case c.MediaType == "application/pdf":
				content = append(content, anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{
					Data: c.Data,
				}))
			}
		case *message.URIContent:
			switch {
			case c.TopLevelMediaType() == "image":
				content = append(content, anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: c.URI}))
			case c.MediaType == "application/pdf":
				content = append(content, anthropic.NewDocumentBlock(anthropic.URLPDFSourceParam{URL: c.URI}))
			}
		case *message.HostedFileContent:
			// The stable Anthropic Messages API used here (anthropic.MessageNewParams)
			// has no file-id image/document source in anthropic-sdk-go v1.58.1; only
			// the Beta API exposes BetaFileImageSourceParam/BetaFileDocumentSourceParam.
			// A hosted file reference therefore cannot be forwarded yet.
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
