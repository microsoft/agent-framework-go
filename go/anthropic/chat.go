// Copyright (c) Microsoft. All rights reserved.

package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
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
		Streaming:        true,
		StructuredOutput: false,
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

		contents := make([]message.Content, 0, len(resp.Content))
		for _, c := range resp.Content {
			contents = a.buildBlock(c.AsAny(), contents)
		}
		contents = append(contents, &message.UsageContent{
			Details: message.UsageDetails{
				InputTokenCount:  resp.Usage.InputTokens,
				OutputTokenCount: resp.Usage.OutputTokens,
			},
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

		var currentMessageID string
		var currentModel string
		var currentRole message.Role

		for stream.Next() {
			event := stream.Current()

			var contents []message.Content
			var finishReason string
			switch event := event.AsAny().(type) {
			case anthropic.MessageStartEvent:
				currentMessageID = event.Message.ID
				currentModel = string(event.Message.Model)
				currentRole = message.RoleAssistant
			case anthropic.ContentBlockStartEvent:
				contents = a.buildBlock(event.ContentBlock.AsAny(), contents)
			case anthropic.ContentBlockDeltaEvent:
				contents = a.buildDelta(event.Delta.AsAny(), contents)
			case anthropic.MessageDeltaEvent:
				finishReason = string(event.Delta.StopReason)
			}

			if !yield(&chatclient.ChatResponseUpdate{
				Contents:          contents,
				Role:              currentRole,
				ResponseID:        currentMessageID,
				MessageID:         currentMessageID,
				ModelID:           currentModel,
				FinishReason:      finishReason,
				CreatedAt:         time.Now(),
				RawRepresentation: event,
			}, nil) {
				return
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, err)
		}
	}
}

func (a *client) buildBlock(v any, contents []message.Content) []message.Content {
	switch v := v.(type) {
	case anthropic.TextBlock:
		contents = append(contents, &message.TextContent{Text: v.Text})
	case anthropic.ToolUseBlock:
		contents = append(contents, &message.FunctionCallContent{
			CallID:    v.ID,
			Name:      v.Name,
			Arguments: v.Input,
		})
	}
	return contents
}

func (a *client) buildDelta(v any, contents []message.Content) []message.Content {
	switch d := v.(type) {
	case anthropic.TextDelta:
		contents = append(contents, &message.TextContent{Text: d.Text})
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
					if reqList, ok := reqs.([]interface{}); ok {
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
