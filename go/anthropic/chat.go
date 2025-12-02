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

func (a *client) Response(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) (*chatclient.ChatResponse, error) {
	params, err := a.buildMessageParams(opts, messages...)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	contents := make([]message.Content, 0, len(resp.Content))
	for _, c := range resp.Content {
		switch c.Type {
		case "text":
			contents = append(contents, &message.TextContent{Text: c.Text})
		case "tool_use":
			contents = append(contents, &message.FunctionCallContent{
				CallID:    c.ID,
				Name:      c.Name,
				Arguments: c.Input,
			})
		}
	}

	return &chatclient.ChatResponse{
		Messages:     []*message.Message{{Role: message.RoleAssistant, Contents: contents}},
		ID:           resp.ID,
		FinishReason: string(resp.StopReason),
		ModelID:      string(resp.Model),
		Usage: &message.UsageDetails{
			InputTokenCount:  resp.Usage.InputTokens,
			OutputTokenCount: resp.Usage.OutputTokens,
		},
	}, nil
}

func (a *client) StreamingResponse(ctx context.Context, opts *chatclient.ChatOptions, messages ...*message.Message) iter.Seq2[*chatclient.ChatResponseUpdate, error] {
	return func(yield func(*chatclient.ChatResponseUpdate, error) bool) {
		params, err := a.buildMessageParams(opts, messages...)
		if err != nil {
			yield(nil, err)
			return
		}

		stream := a.client.Messages.NewStreaming(ctx, params)

		var currentMessageID string
		var currentModel string
		var currentRole message.Role

		for stream.Next() {
			event := stream.Current()

			var contents []message.Content
			var finishReason string

			switch event.Type {
			case "message_start":
				currentMessageID = event.Message.ID
				currentModel = string(event.Message.Model)
				currentRole = message.Role(event.Message.Role)
			case "content_block_start":
				if event.ContentBlock.Type == "tool_use" {
					contents = append(contents, &message.FunctionCallContent{
						CallID:    event.ContentBlock.ID,
						Name:      event.ContentBlock.Name,
						Arguments: event.ContentBlock.Input,
					})
				}
			case "content_block_delta":
				if event.Delta.Type == "text_delta" {
					contents = append(contents, &message.TextContent{Text: event.Delta.Text})
				}
			case "message_delta":
				if event.Delta.StopReason != "" {
					finishReason = string(event.Delta.StopReason)
				}
			}

			if len(contents) > 0 || finishReason != "" {
				resp := &chatclient.ChatResponseUpdate{
					Contents:     contents,
					Role:         currentRole,
					ResponseID:   currentMessageID,
					MessageID:    currentMessageID,
					ModelID:      currentModel,
					FinishReason: finishReason,
					CreatedAt:    time.Now(),
				}
				if !yield(resp, nil) {
					return
				}
			}
		}

		if err := stream.Err(); err != nil {
			yield(nil, err)
		}
	}
}

func (a *client) buildMessageParams(options *chatclient.ChatOptions, messages ...*message.Message) (anthropic.MessageNewParams, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(a.config.Model),
		Messages:  []anthropic.MessageParam{},
		MaxTokens: 1024, // Default max tokens
	}

	if options != nil {
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

	if msg.Role == message.RoleAssistant {
		return anthropic.NewAssistantMessage(content...), nil
	} else if msg.Role == message.RoleTool {
		// Tool results are user messages in Anthropic
		return anthropic.NewUserMessage(content...), nil
	}

	return anthropic.NewUserMessage(content...), nil
}
