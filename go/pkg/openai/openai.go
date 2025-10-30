// Copyright (c) Microsoft. All rights reserved.

package openai

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework/go/pkg/agent"
	"github.com/microsoft/agent-framework/go/pkg/internal/exp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

var _ agent.Agent = (*Agent)(nil)
var _ agent.StreamableAgent = (*Agent)(nil)

type Agent struct {
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

	Name         string
	Instructions string
	ID           string
	Options      *agent.RunOptions // Default options for the agent.
}

func newAgent(isAzure bool, config AgentConfig) *Agent {
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
	if config.ID == "" {
		config.ID = uuid.New().String()
	}
	return &Agent{
		client: client,
		config: config,
	}
}

// NewAgent creates a new Agent.
func NewAgent(config AgentConfig) *Agent {
	return newAgent(false, config)
}

// NewAzureAgent creates a new [Agent].
func NewAzureAgent(config AgentConfig) *Agent {
	return newAgent(true, config)
}

// ID returns the agent's unique identifier.
func (a *Agent) ID() string {
	return a.config.ID
}

// Name returns the agent's name.
func (a *Agent) Name() string {
	return a.config.Name
}

// NewThread creates a new thread for this agent.
func (a *Agent) NewThread() agent.Thread {
	return agent.NewInMemoryThread()
}

// DeserializeThread deserializes a thread from JSON.
func (a *Agent) DeserializeThread(data []byte) (agent.Thread, error) {
	// TODO: Implement JSON deserialization
	return agent.NewInMemoryThread(), nil
}

// Run executes the agent with the given messages and options.
func (a *Agent) Run(ctx context.Context, t agent.Thread, options *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
	messages = slices.Clone(messages)
	inLength := len(messages)

	// Prepare messages with system instructions
	messages = a.prepareMessages(messages)

	// Convert RunOptions to ChatOptions
	options = a.mergeOptions(options)

	for {
		// Call the chat client
		response, err := a.run(ctx, options, messages...)
		if err != nil {
			return nil, err
		}
		message := response.Messages[0]
		messages = append(messages, message)
		toolResult := exp.RunToolCalls(ctx, options, message.Contents...)
		if len(toolResult) > 0 {
			// Add a single Message to the response with the results
			messages = append(messages, agent.NewMessage(agent.RoleTool, toolResult...))
			continue
		}
		return &agent.RunResponse{
			Messages:   messages[inLength:],
			AgentID:    a.ID(),
			ResponseID: response.ResponseID,
		}, nil
	}
}

// Run generates a single response for the given messages.
func (c *Agent) run(ctx context.Context, options *agent.RunOptions, messages ...*agent.Message) (*agent.RunResponse, error) {
	resp, err := c.client.Chat.Completions.New(ctx, buildCompletionParams(c.config.Model, options, messages...))
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
		Messages: []*agent.Message{agent.NewMessage(agent.Role(choice.Message.Role), contents...)},
	}, nil
}

func (a *Agent) RunStream(ctx context.Context, t agent.Thread, options *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	messages = slices.Clone(messages)

	// Prepare messages with system instructions
	messages = a.prepareMessages(messages)

	// Convert RunOptions to ChatOptions
	options = a.mergeOptions(options)

	return func(yield func(*agent.RunResponseUpdate, error) bool) {
		for {
			var contents []agent.Content
			for update, err := range a.runStream(ctx, options, messages...) {
				if err != nil {
					if !yield(nil, err) {
						return
					}
				}
				contents = append(contents, update.Contents...)
				if !yield(update, nil) {
					return
				}
			}
			if !slices.ContainsFunc(contents, func(content agent.Content) bool {
				_, ok := content.(*agent.FunctionCallContent)
				return ok
			}) {
				// No tool calls
				return
			}
			messages = append(messages, agent.NewMessage(agent.RoleAssistant, contents...))
			toolResult := exp.RunToolCalls(ctx, options, contents...)
			if len(toolResult) > 0 {
				// Add a single Message to the response with the results
				if !yield(&agent.RunResponseUpdate{
					AgentID:  a.ID(),
					Contents: toolResult,
					Role:     agent.RoleAssistant,
				}, nil) {
					return
				}
				messages = append(messages, agent.NewMessage(agent.RoleTool, toolResult...))
				continue
			}
			// No more tool calls to process
			return
		}
	}

}

func (a *Agent) runStream(ctx context.Context, options *agent.RunOptions, messages ...*agent.Message) iter.Seq2[*agent.RunResponseUpdate, error] {
	stream := a.client.Chat.Completions.NewStreaming(ctx, buildCompletionParams(a.config.Model, options, messages...))
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
			if choice := chunk.Choices[0]; choice.Delta.Content != "" {
				contents = append(contents, &agent.TextContent{Text: choice.Delta.Content})
			}
			resp := &agent.RunResponseUpdate{
				Contents: contents,
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

// prepareMessages adds system instructions to the message list.
func (a *Agent) prepareMessages(messages []*agent.Message) []*agent.Message {
	if a.config.Instructions == "" {
		return messages
	}

	return append(messages, agent.NewMessage(agent.RoleSystem, &agent.TextContent{Text: a.config.Instructions}))
}

// mergeOptions merges the provided RunOptions with the agent's default options.
func (a *Agent) mergeOptions(options *agent.RunOptions) *agent.RunOptions {
	if options == nil {
		return a.config.Options
	}
	opts := &agent.RunOptions{
		Tools:              options.Tools,
		ToolMode:           options.ToolMode,
		Temperature:        options.Temperature,
		TopP:               options.TopP,
		MaxTokens:          options.MaxTokens,
		AdditionalMetadata: options.AdditionalMetadata,
	}
	if baseOptions := a.config.Options; baseOptions != nil {
		// Fill in any missing fields from base options.
		if opts.Tools == nil {
			opts.Tools = baseOptions.Tools
		}
		cmp.Or(opts.ToolMode, baseOptions.ToolMode)
		cmp.Or(opts.Temperature, baseOptions.Temperature)
		cmp.Or(opts.TopP, baseOptions.TopP)
		cmp.Or(opts.MaxTokens, baseOptions.MaxTokens)
		for k, v := range baseOptions.AdditionalMetadata {
			if _, exists := opts.AdditionalMetadata[k]; !exists {
				opts.AdditionalMetadata[k] = v
			}
		}
	}

	return opts
}

// buildCompletionParams constructs the parameters for the OpenAI chat completion API.
func buildCompletionParams(model string, options *agent.RunOptions, messages ...*agent.Message) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:    model,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)),
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
			case *agent.Func:
				args := make(map[string]any, len(tool.Parameters))
				for _, param := range tool.Parameters {
					args[param.Name] = map[string]any{
						"type":        param.Type,
						"description": param.Description,
					}
				}
				params.Tools = append(params.Tools, openai.ChatCompletionToolUnionParam{
					OfFunction: &openai.ChatCompletionFunctionToolParam{
						Function: shared.FunctionDefinitionParam{
							Name:        tool.Name,
							Description: openai.String(tool.Description),
							Parameters: map[string]any{
								"type":       "object",
								"properties": args,
							},
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
		return openai.ChatCompletionMessageParamUnion{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: openai.String(extractText(msg)),
				},
			},
		}

	case agent.RoleUser:
		return openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.String(extractText(msg)),
				},
			},
		}

	case agent.RoleAssistant:
		// Check if the message contains tool calls
		toolCalls := extractToolCalls(msg)
		return openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(extractText(msg)),
				},
				ToolCalls: toolCalls,
			},
		}

	case agent.RoleTool:
		// Tool messages contain function results
		toolResults := extractToolResults(msg)
		return openai.ChatCompletionMessageParamUnion{
			OfTool: &openai.ChatCompletionToolMessageParam{
				Content: openai.ChatCompletionToolMessageParamContentUnion{
					OfString: openai.String(toolResults),
				},
				ToolCallID: extractToolCallID(msg),
			},
		}

	default:
		panic("unknown message role: " + string(msg.Role))
	}
}

// extractText extracts text content from a message.
func extractText(msg *agent.Message) string {
	var text string
	for _, content := range msg.Contents {
		if tc, ok := content.(*agent.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

// extractToolCalls extracts function call content from a message.
func extractToolCalls(msg *agent.Message) []openai.ChatCompletionMessageToolCallUnionParam {
	var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
	for _, content := range msg.Contents {
		if fc, ok := content.(*agent.FunctionCallContent); ok {
			// Marshal arguments to JSON
			argsJSON, err := json.Marshal(fc.Arguments)
			if err != nil {
				continue
			}

			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: fc.CallID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      fc.Name,
						Arguments: string(argsJSON),
					},
				},
			})
		}
	}
	return toolCalls
}

// extractToolResults extracts function result content from a message and formats it.
func extractToolResults(msg *agent.Message) string {
	for _, content := range msg.Contents {
		if fr, ok := content.(*agent.FunctionResultContent); ok {
			if fr.Error != nil {
				return fmt.Sprintf("Error: %v", fr.Error)
			}
			return fmt.Sprintf("%v", fr.Result)
		}
	}
	return ""
}

// extractToolCallID extracts the first tool call ID from function result content.
func extractToolCallID(msg *agent.Message) string {
	for _, content := range msg.Contents {
		if fr, ok := content.(*agent.FunctionResultContent); ok {
			return fr.CallID
		}
	}
	return ""
}
