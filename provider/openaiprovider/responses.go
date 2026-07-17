// Copyright (c) Microsoft. All rights reserved.

package openaiprovider

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/format/jsonformat"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// NewAgent creates an agent backed by the OpenAI API that best fits the Agent
// Framework. The underlying OpenAI API is an implementation detail and can
// change at any time. It currently uses [NewResponsesAgent].
func NewAgent(oclient openai.Client, config AgentConfig) *agent.Agent {
	return NewResponsesAgent(oclient, config)
}

// NewResponsesAgent creates an agent backed by the OpenAI Responses API.
func NewResponsesAgent(oclient openai.Client, config AgentConfig) *agent.Agent {
	c := &responsesClient{
		client: oclient,
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
	return agent.New(
		agent.ProviderConfig{
			ProviderName: "openai",
			Run:          c.run,
			Middlewares:  providerMiddlewares,
			Format:       c.formatOf,
			Unmarshal:    c.unmarshal,
		}, config.Config)
}

type responsesClient struct {
	client openai.Client
	config AgentConfig
}

type responsesNewParamsOpt responses.ResponseNewParams

func (o responsesNewParamsOpt) Value() any {
	return responses.ResponseNewParams(o)
}

// ResponsesNewParams allows passing custom parameters to the underlying OpenAI Responses API calls.
func ResponsesNewParams(params responses.ResponseNewParams) agent.Option {
	return responsesNewParamsOpt(params)
}

func (a *responsesClient) formatOf(v any) (agent.ResponseFormat, error) {
	return jsonformat.ForType(reflect.TypeOf(v))
}

func (a *responsesClient) unmarshal(format agent.ResponseFormat, data []byte, v any) error {
	jsonFormat, err := jsonformat.FromResponseFormat(format)
	if err != nil {
		return err
	}
	return jsonFormat.Unmarshal(data, v)
}

func (a *responsesClient) run(ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		stream, _ := agent.GetOption(options, agent.Stream)

		// Get session for conversation ID management
		var session *agent.Session
		var keepConversationID bool // true if we should keep the conversation ID unchanged (it's a "conv_" ID)
		if t, ok := agent.GetOption(options, agent.WithSession); ok && t != nil {
			session = t
			keepConversationID = session.ServiceID() != "" && strings.HasPrefix(session.ServiceID(), "conv_")
		}
		disableStoreOutput := responsesDisableStoreOutput(a.config, options)

		// Helper to update conversation ID after response completes
		updateConversationID := func(responseID string) {
			if disableStoreOutput {
				return
			}
			if session != nil && !keepConversationID && responseID != "" {
				session.SetServiceID(responseID)
			}
		}

		// Handle continuation token for resuming background responses
		if token, ok := agent.GetOption(options, agent.WithContinuationToken); ok && token != "" {
			if len(messages) > 0 {
				yield(nil, errors.New("messages are not allowed when continuing a background response using a continuation token"))
				return
			}
			var ct continuationToken
			if err := json.Unmarshal([]byte(token), &ct); err != nil {
				yield(nil, fmt.Errorf("failed to parse continuation token: %w", err))
				return
			}

			if stream {
				// Get streaming response
				streamResp := a.client.Responses.GetStreaming(ctx, ct.ResponseID, responses.ResponseGetParams{
					StartingAfter: openai.Int(ct.SequenceNumber),
				}, telemetryRequestOption)
				// Update conversation ID when resuming
				updateConversationID(ct.ResponseID)
				for streamResp.Next() {
					update, err := responsesProcessStreamingUpdate(streamResp.Current(), ct.ResponseID, true)
					if err != nil {
						yield(nil, err)
						return
					}
					if update != nil {
						if !yield(update, nil) {
							return
						}
					}
				}
				if streamResp.Err() != nil {
					yield(nil, streamResp.Err())
				}
			} else {
				// Get complete response
				resp, err := a.client.Responses.Get(ctx, ct.ResponseID, responses.ResponseGetParams{}, telemetryRequestOption)
				if err != nil {
					yield(nil, err)
					return
				}
				// Update conversation ID when resuming
				updateConversationID(ct.ResponseID)
				responsesProcessResponse(resp, ct.SequenceNumber, yield)
			}
			return
		}

		// Build request parameters
		body, err := responsesBuildCompletionParams(a.config, messages, options)
		if err != nil {
			yield(nil, err)
			return
		}

		if stream {
			// Create streaming response
			streamResp := a.client.Responses.NewStreaming(ctx, body, telemetryRequestOption)
			responseID := ""
			createdAt := time.Time{}
			isBackground, _ := agent.GetOption(options, agent.AllowBackgroundResponses)
			for streamResp.Next() {
				update, err := responsesProcessStreamingUpdate(streamResp.Current(), responseID, isBackground)
				if err != nil {
					yield(nil, err)
					return
				}
				if update != nil {
					// Capture responseID and createdAt from the first event
					if responseID == "" && update.ResponseID != "" {
						responseID = update.ResponseID
						// Update conversation ID when we get the response ID
						updateConversationID(responseID)
					}
					if createdAt.IsZero() && !update.CreatedAt.IsZero() {
						createdAt = update.CreatedAt
					}
					// Ensure all updates have responseID and createdAt set
					if update.ResponseID == "" {
						update.ResponseID = responseID
					}
					if update.CreatedAt.IsZero() {
						update.CreatedAt = createdAt
					}
					if !yield(update, nil) {
						return
					}
				}
			}
			if streamResp.Err() != nil {
				yield(nil, streamResp.Err())
			}
		} else {
			// Create complete response
			resp, err := a.client.Responses.New(ctx, body, telemetryRequestOption)
			if err != nil {
				yield(nil, err)
				return
			}
			// Update conversation ID with the response ID
			updateConversationID(resp.ID)
			responsesProcessResponse(resp, 0, yield)
		}
	}
}

// buildCompletionParams constructs the parameters for the OpenAI chat completion API.
func responsesBuildCompletionParams(config AgentConfig, messages []*message.Message, opts []agent.Option) (responses.ResponseNewParams, error) {
	var params responses.ResponseNewParams
	if p, ok := agent.GetOption(opts, ResponsesNewParams); ok {
		params = p
	}
	if responsesDisableStoreOutput(config, opts) {
		if param.IsOmitted(params.Store) {
			params.Store = openai.Bool(false)
		}
		if !slices.Contains(params.Include, responses.ResponseIncludableReasoningEncryptedContent) {
			params.Include = append(params.Include, responses.ResponseIncludableReasoningEncryptedContent)
		}
	}
	params.Model = cmp.Or(params.Model, config.Model)
	instructions := slices.Collect(agent.AllOptions(opts, agent.WithInstructions))
	if len(instructions) > 0 {
		params.Instructions = openai.String(strings.Join(instructions, "\n"))
	}
	if v, ok := agent.GetOption(opts, agent.AllowBackgroundResponses); ok {
		params.Background = openai.Bool(v)
	}
	if session, ok := agent.GetOption(opts, agent.WithSession); ok && session != nil {
		if session.ServiceID() != "" {
			// Technically, OpenAI's IDs are opaque. However, by convention conversation IDs start with "conv_" and
			// we can use that to disambiguate whether we're looking at a conversation ID or a response ID.
			if strings.HasPrefix(session.ServiceID(), "conv_") {
				params.Conversation = responses.ResponseNewParamsConversationUnion{
					OfString: openai.String(session.ServiceID()),
				}
			} else {
				params.PreviousResponseID = openai.String(session.ServiceID())
			}
		}
	}

	if frmt, ok := agent.GetOption(opts, agent.WithResponseFormat); ok {
		switch frmt.Kind {
		case "json":
			if schema := frmt.Schema; schema != nil {
				schemaMap, err := schemaToMap(schema)
				if err != nil {
					return responses.ResponseNewParams{}, fmt.Errorf("failed to convert response format schema (type %T) to JSON format: %w", schema, err)
				}
				params.Text.Format.OfJSONSchema = &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name:   frmt.Name,
					Schema: schemaMap,
				}
				if desc := frmt.Description; desc != "" {
					params.Text.Format.OfJSONSchema.Description = openai.String(desc)
				}
				if frmt.Strict {
					params.Text.Format.OfJSONSchema.Strict = openai.Bool(true)
				}
			} else {
				// Fallback to generic JSON object format
				param := shared.NewResponseFormatJSONObjectParam()
				params.Text.Format.OfJSONObject = &param
			}
		}
	}
	first := true
	for tl := range agent.AllOptions(opts, agent.WithTool) {
		if first {
			first = false
			if mode, ok := agent.GetOption(opts, agent.WithToolMode); ok {
				switch mode.Mode() {
				case tool.ToolModeAuto, "":
					params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
						OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
					}
				case tool.ToolModeNone:
					params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
						OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsNone),
					}
				case tool.ToolModeRequired:
					names := mode.Required()
					if len(names) == 0 {
						params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
							OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsRequired),
						}
					} else if len(names) == 1 {
						params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
							OfFunctionTool: &responses.ToolChoiceFunctionParam{
								Name: names[0],
							},
						}
					} else {
						toolsMap := make([]map[string]any, 0, len(names))
						for _, name := range names {
							toolsMap = append(toolsMap, map[string]any{
								"type": "function",
								"name": name,
							})
						}
						params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
							OfAllowedTools: &responses.ToolChoiceAllowedParam{
								Mode:  responses.ToolChoiceAllowedModeRequired,
								Tools: toolsMap,
							},
						}
					}
				}
			}
		}
		switch tl := tl.(type) {
		case tool.FuncTool:
			name, description := tl.Name(), tl.Description()
			schema := tl.Schema()
			funcParams, err := schemaToMap(schema)
			if err != nil {
				return responses.ResponseNewParams{}, fmt.Errorf("failed to convert schema for function tool %q (type %T) to JSON format: %w", name, schema, err)
			}
			params.Tools = append(params.Tools, responses.ToolUnionParam{
				OfFunction: &responses.FunctionToolParam{
					Name:        name,
					Description: openai.String(description),
					Parameters:  funcParams,
				},
			})
		case *hostedtool.WebSearch:
			var variant responses.WebSearchToolParam
			variant.Type = responses.WebSearchToolTypeWebSearch
			if location, ok := tl.AdditionalProperties["user_location"]; ok {
				if location, ok := location.(map[string]string); ok {
					if city, ok := location["city"]; ok && city != "" {
						variant.UserLocation.City = openai.String(city)
					}
					if region, ok := location["region"]; ok && region != "" {
						variant.UserLocation.Region = openai.String(region)
					}
					if country, ok := location["country"]; ok && country != "" {
						variant.UserLocation.Country = openai.String(country)
					}
					if timezone, ok := location["timezone"]; ok && timezone != "" {
						variant.UserLocation.Timezone = openai.String(timezone)
					}
				}
			}
			if contextSize, ok := tl.AdditionalProperties["search_context_size"]; ok {
				if contextSize, ok := contextSize.(string); ok && contextSize != "" {
					variant.SearchContextSize = responses.WebSearchToolSearchContextSize(contextSize)
				}
			}
			if filters, ok := tl.AdditionalProperties["filters"]; ok {
				if filters, ok := filters.(map[string]any); ok {
					if domains, ok := filters["allowed_domains"]; ok {
						if domains, ok := domains.([]string); ok {
							variant.Filters.AllowedDomains = domains
						}
					}
				}
			}
			params.Tools = append(params.Tools, responses.ToolUnionParam{
				OfWebSearch: &variant,
			})
		case *hostedtool.FileSearch:
			var variant responses.FileSearchToolParam
			if tl.MaximumResultCount != 0 {
				variant.MaxNumResults = openai.Int(int64(tl.MaximumResultCount))
			}
			for _, input := range tl.Inputs {
				if hosted, ok := input.(*message.HostedVectorStoreContent); ok {
					variant.VectorStoreIDs = append(variant.VectorStoreIDs, hosted.VectorStoreID)
					continue
				}
			}
			params.Tools = append(params.Tools, responses.ToolUnionParam{
				OfFileSearch: &variant,
			})
		case *hostedtool.CodeInterpreter:
			var variant responses.ToolCodeInterpreterParam
			hosted := make([]string, 0, len(tl.Inputs))
			for _, input := range tl.Inputs {
				if hf, ok := input.(*message.HostedFileContent); ok {
					hosted = append(hosted, hf.FileID)
				}
			}
			if len(hosted) == 1 {
				variant.Container.OfString = openai.String(hosted[0])
			} else if len(hosted) > 1 {
				variant.Container.OfCodeInterpreterToolAuto = &responses.ToolCodeInterpreterContainerCodeInterpreterContainerAutoParam{
					FileIDs: hosted,
				}
			} else {
				// Default to auto container when no files are specified
				variant.Container.OfCodeInterpreterToolAuto = &responses.ToolCodeInterpreterContainerCodeInterpreterContainerAutoParam{}
			}
			params.Tools = append(params.Tools, responses.ToolUnionParam{
				OfCodeInterpreter: &variant,
			})
		case *hostedtool.MCPServer:
			var variant responses.ToolMcpParam
			variant.ServerLabel = tl.ServerName
			if _, err := url.Parse(tl.ServerAddress); err == nil {
				variant.ServerURL = openai.String(tl.ServerAddress)
			} else {
				variant.ConnectorID = tl.ServerAddress
			}
			if tl.ServerDescription != "" {
				variant.ServerDescription = openai.String(tl.ServerDescription)
			}
			variant.AllowedTools.OfMcpAllowedTools = tl.AllowedTools
			variant.Headers = tl.Headers
			if tl.Authorization != "" {
				variant.Authorization = openai.String(tl.Authorization)
			}
			params.Tools = append(params.Tools, responses.ToolUnionParam{
				OfMcp: &variant,
			})
		}
	}
	for _, msg := range messages {
		var err error
		params.Input.OfInputItemList, err = responsesBuildMessageParam(msg, params.Input.OfInputItemList)
		if err != nil {
			return responses.ResponseNewParams{}, err
		}
	}
	return params, nil
}

func responsesDisableStoreOutput(config AgentConfig, opts []agent.Option) bool {
	if p, ok := agent.GetOption(opts, ResponsesNewParams); ok && !param.IsOmitted(p.Store) {
		return !p.Store.Or(true)
	}
	return config.DisableStoreOutput
}

// responsesBuildMessageParam converts an agent.Message to one or more OpenAI message parameters.
// Returns a slice because some agent messages (like RoleTool) need to be split into multiple OpenAI messages.
func responsesBuildMessageParam(msg *message.Message, resp responses.ResponseInputParam) (responses.ResponseInputParam, error) {
	var contents responses.ResponseInputMessageContentListParam
	switch msg.Role {
	case message.RoleSystem:
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok {
				contents = append(contents, responses.ResponseInputContentUnionParam{
					OfInputText: &responses.ResponseInputTextParam{
						Text: tc.Text,
					},
				})
			}
		}
	case message.RoleUser:
		for _, c := range msg.Contents {
			switch c := c.(type) {
			case *message.TextContent:
				contents = append(contents, responses.ResponseInputContentParamOfInputText(c.Text))
			case *message.URIContent:
				switch c.TopLevelMediaType() {
				case "image":
					contents = append(contents, responses.ResponseInputContentUnionParam{
						OfInputImage: &responses.ResponseInputImageParam{
							ImageURL: openai.String(c.URI),
							Detail:   responses.ResponseInputImageDetail(imageDetail(c.AdditionalProperties)),
						},
					})
				default:
					contents = append(contents, responses.ResponseInputContentUnionParam{
						OfInputFile: &responses.ResponseInputFileParam{
							FileURL: openai.String(c.URI),
						},
					})
				}
			case *message.DataContent:
				switch c.TopLevelMediaType() {
				case "image":
					contents = append(contents, responses.ResponseInputContentUnionParam{
						OfInputImage: &responses.ResponseInputImageParam{
							ImageURL: openai.String(c.URI()),
							Detail:   responses.ResponseInputImageDetail(imageDetail(c.AdditionalProperties)),
						},
					})
				default:
					file := responses.ResponseInputFileParam{
						FileData: openai.String(c.URI()),
					}
					if c.Name != "" {
						file.Filename = openai.String(c.Name)
					}
					contents = append(contents, responses.ResponseInputContentUnionParam{OfInputFile: &file})
				}
			case *message.HostedFileContent:
				file := responses.ResponseInputFileParam{FileID: openai.String(c.FileID)}
				if c.Name != "" {
					file.Filename = openai.String(c.Name)
				}
				contents = append(contents, responses.ResponseInputContentUnionParam{
					OfInputFile: &file,
				})
			}
		}

	case message.RoleAssistant:
		var outputContents []responses.ResponseOutputMessageContentUnionParam
		outputGroup := 0
		flushOutputContents := func() {
			if len(outputContents) == 0 {
				return
			}
			id := msg.ID
			if id == "" || outputGroup > 0 {
				id = fmt.Sprintf("msg_local_%d", len(resp))
			}
			resp = append(resp, responses.ResponseInputItemParamOfOutputMessage(outputContents, id, responses.ResponseOutputMessageStatusCompleted))
			outputContents = nil
			outputGroup++
		}
		for _, c := range msg.Contents {
			switch c := c.(type) {
			case *message.TextContent:
				outputContents = append(outputContents, responses.ResponseOutputMessageContentUnionParam{
					OfOutputText: &responses.ResponseOutputTextParam{
						// TODO: Convert message annotations back to Responses output-text annotations.
						Annotations: []responses.ResponseOutputTextAnnotationUnionParam{},
						Text:        c.Text,
					},
				})
			case *message.TextReasoningContent:
				flushOutputContents()
				// Reasoning content is added as a separate input item
				var reasoning responses.ResponseReasoningItemParam
				if c.ProtectedData != "" {
					reasoning.EncryptedContent = openai.String(c.ProtectedData)
				}
				reasoning.Content = append(reasoning.Content, responses.ResponseReasoningItemContentParam{
					Text: c.Text,
				})
				resp = append(resp, responses.ResponseInputItemUnionParam{
					OfReasoning: &reasoning,
				})
			case *message.FunctionCallContent:
				flushOutputContents()
				resp = append(resp, responses.ResponseInputItemParamOfFunctionCall(c.Arguments, c.CallID, c.Name))
			}
		}
		flushOutputContents()

	case message.RoleTool:
		for _, c := range msg.Contents {
			if funcResult, ok := c.(*message.FunctionResultContent); ok {
				ret := funcResult.Result
				var outputContent responses.ResponseFunctionCallOutputItemListParam

				if funcResult.Error != nil {
					// Error case - serialize as text with "Error: " prefix
					resp = append(resp, responses.ResponseInputItemParamOfFunctionCallOutput(
						funcResult.CallID,
						fmt.Sprintf("Error: %v", funcResult.Error),
					))
				} else if b, ok := ret.(json.RawMessage); ok {
					// json.RawMessage - pass as string directly
					resp = append(resp, responses.ResponseInputItemParamOfFunctionCallOutput(
						funcResult.CallID,
						string(b),
					))
				} else if str, ok := ret.(string); ok {
					// Plain string - pass directly
					resp = append(resp, responses.ResponseInputItemParamOfFunctionCallOutput(
						funcResult.CallID,
						str,
					))
				} else if singleContent, ok := ret.(message.Content); ok {
					// Handle single Content item
					switch c := singleContent.(type) {
					case *message.TextContent:
						outputContent = responses.ResponseFunctionCallOutputItemListParam{
							{
								OfInputText: &responses.ResponseInputTextContentParam{
									Text: c.Text,
								},
							},
						}
					case *message.DataContent:
						dataURI := c.URI()
						if c.TopLevelMediaType() == "image" {
							outputContent = responses.ResponseFunctionCallOutputItemListParam{
								{
									OfInputImage: &responses.ResponseInputImageContentParam{
										ImageURL: param.NewOpt(dataURI),
									},
								},
							}
						} else {
							outputContent = responses.ResponseFunctionCallOutputItemListParam{
								{
									OfInputFile: &responses.ResponseInputFileContentParam{
										FileData: param.NewOpt(dataURI),
										Filename: param.NewOpt(c.Name),
									},
								},
							}
						}
					case *message.URIContent:
						if c.TopLevelMediaType() == "image" {
							outputContent = responses.ResponseFunctionCallOutputItemListParam{
								{
									OfInputImage: &responses.ResponseInputImageContentParam{
										ImageURL: param.NewOpt(c.URI),
									},
								},
							}
						} else {
							outputContent = responses.ResponseFunctionCallOutputItemListParam{
								{
									OfInputFile: &responses.ResponseInputFileContentParam{
										FileURL: param.NewOpt(c.URI),
									},
								},
							}
						}
					case *message.HostedFileContent:
						if c.TopLevelMediaType() == "image" {
							outputContent = responses.ResponseFunctionCallOutputItemListParam{
								{
									OfInputImage: &responses.ResponseInputImageContentParam{
										FileID: param.NewOpt(c.FileID),
									},
								},
							}
						} else {
							outputContent = responses.ResponseFunctionCallOutputItemListParam{
								{
									OfInputFile: &responses.ResponseInputFileContentParam{
										FileID:   param.NewOpt(c.FileID),
										Filename: param.NewOpt(c.Name),
									},
								},
							}
						}
					default:
						outputContent = responses.ResponseFunctionCallOutputItemListParam{
							{
								OfInputText: &responses.ResponseInputTextContentParam{
									Text: fmt.Sprintf("%v", c),
								},
							},
						}
					}
					resp = append(resp, responses.ResponseInputItemParamOfFunctionCallOutput(
						funcResult.CallID,
						outputContent,
					))
				} else if contentSlice, ok := ret.([]message.Content); ok {
					// Handle slice of Content - convert each content item
					for _, content := range contentSlice {
						switch c := content.(type) {
						case *message.TextContent:
							outputContent = append(outputContent, responses.ResponseFunctionCallOutputItemUnionParam{
								OfInputText: &responses.ResponseInputTextContentParam{
									Text: c.Text,
								},
							})
						case *message.DataContent:
							// DataContent should be converted to input_image (for images) or input_file (for non-images)
							dataURI := fmt.Sprintf("data:%s;base64,%s", c.MediaType, c.Data)
							if c.TopLevelMediaType() == "image" {
								outputContent = append(outputContent, responses.ResponseFunctionCallOutputItemUnionParam{
									OfInputImage: &responses.ResponseInputImageContentParam{
										ImageURL: param.NewOpt(dataURI),
									},
								})
							} else {
								outputContent = append(outputContent, responses.ResponseFunctionCallOutputItemUnionParam{
									OfInputFile: &responses.ResponseInputFileContentParam{
										FileData: param.NewOpt(dataURI),
										Filename: param.NewOpt(c.Name),
									},
								})
							}
						case *message.URIContent:
							// URIContent should be converted to input_image (for images) or input_file (for non-images)
							if c.TopLevelMediaType() == "image" {
								outputContent = append(outputContent, responses.ResponseFunctionCallOutputItemUnionParam{
									OfInputImage: &responses.ResponseInputImageContentParam{
										ImageURL: param.NewOpt(c.URI),
									},
								})
							} else {
								outputContent = append(outputContent, responses.ResponseFunctionCallOutputItemUnionParam{
									OfInputFile: &responses.ResponseInputFileContentParam{
										FileURL: param.NewOpt(c.URI),
									},
								})
							}
						case *message.HostedFileContent:
							// HostedFileContent should be converted to input_image (for images) or input_file (for non-images)
							if c.TopLevelMediaType() == "image" {
								outputContent = append(outputContent, responses.ResponseFunctionCallOutputItemUnionParam{
									OfInputImage: &responses.ResponseInputImageContentParam{
										FileID: param.NewOpt(c.FileID),
									},
								})
							} else {
								outputContent = append(outputContent, responses.ResponseFunctionCallOutputItemUnionParam{
									OfInputFile: &responses.ResponseInputFileContentParam{
										FileID:   param.NewOpt(c.FileID),
										Filename: param.NewOpt(c.Name),
									},
								})
							}
						default:
							// Fallback for other content types - convert to text
							outputContent = append(outputContent, responses.ResponseFunctionCallOutputItemUnionParam{
								OfInputText: &responses.ResponseInputTextContentParam{
									Text: fmt.Sprintf("%v", c),
								},
							})
						}
					}
					resp = append(resp, responses.ResponseInputItemParamOfFunctionCallOutput(
						funcResult.CallID,
						outputContent,
					))
				} else {
					// Default case - convert to string (JSON-encode structured
					// results rather than rendering them with Go's %v).
					resp = append(resp, responses.ResponseInputItemParamOfFunctionCallOutput(
						funcResult.CallID,
						toolResultText(ret),
					))
				}

			}
		}

	default:
		panic("unknown message role: " + string(msg.Role))
	}

	if len(contents) > 0 {
		var msgParam responses.ResponseInputItemMessageParam
		msgParam.Content = contents
		msgParam.Role = string(responses.EasyInputMessageRole(msg.Role))
		msgParam.Type = "message"
		resp = append(resp, responses.ResponseInputItemUnionParam{OfInputMessage: &msgParam})
	}
	return resp, nil
}

func schemaToMap(schema any) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}
	if schemaMap, ok := schema.(map[string]any); ok {
		return schemaMap, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var schemaMap map[string]any
	if err := json.Unmarshal(data, &schemaMap); err != nil {
		return nil, err
	}
	return schemaMap, nil
}

func responsesProcessResponse(resp *responses.Response, seqNum int64, yield func(*agent.ResponseUpdate, error) bool) {
	var contToken string
	if resp.Background {
		// Returns a continuation token for in-progress or queued responses as they are not yet complete.
		switch resp.Status {
		case responses.ResponseStatusInProgress, responses.ResponseStatusQueued:
			ct := continuationToken{
				ResponseID:     resp.ID,
				SequenceNumber: seqNum,
			}
			data, err := json.Marshal(ct)
			if err != nil {
				yield(nil, fmt.Errorf("failed to marshal continuation token: %w", err))
				return
			}
			contToken = string(data)
		}
	}

	currentUpdate := &agent.ResponseUpdate{
		ResponseID:           resp.ID,
		FinishReason:         responsesFinishReason(resp),
		CreatedAt:            time.Unix(int64(resp.CreatedAt), 0),
		Role:                 message.RoleAssistant,
		AdditionalProperties: responsesPopulateAdditionalProperties(resp),
	}
	// Only set ContinuationToken if it's not empty
	if contToken != "" {
		currentUpdate.ContinuationToken = contToken
	}

	for _, out := range resp.Output {
		switch out := out.AsAny().(type) {
		case responses.ResponseOutputMessage:
			if currentUpdate.MessageID != "" && currentUpdate.MessageID != out.ID {
				if !yield(currentUpdate, nil) {
					return
				}
				currentUpdate = &agent.ResponseUpdate{}
			}
			currentUpdate.MessageID = out.ID
			currentUpdate.ResponseID = resp.ID
			currentUpdate.FinishReason = responsesFinishReason(resp)
			// Only set ContinuationToken if it's not empty
			if contToken != "" {
				currentUpdate.ContinuationToken = contToken
			}
			currentUpdate.RawRepresentation = out
			currentUpdate.Role = message.Role(out.Role)
			currentUpdate.CreatedAt = time.Unix(int64(resp.CreatedAt), 0)
			for _, c := range out.Content {
				switch c := c.AsAny().(type) {
				case responses.ResponseOutputText:
					textContent := &message.TextContent{Text: c.Text}
					populateAnnotations(c.Annotations, textContent)
					currentUpdate.Contents = append(currentUpdate.Contents, textContent)
				case responses.ResponseOutputRefusal:
					currentUpdate.Contents = append(currentUpdate.Contents, &message.ErrorContent{
						Message:   c.Refusal,
						ErrorCode: "Refusal",
					})
				}
			}

		case responses.ResponseReasoningItem:
			var sb strings.Builder
			for _, c := range out.Content {
				sb.WriteString(c.Text)
			}
			currentUpdate.Contents = append(currentUpdate.Contents, &message.TextReasoningContent{
				Text:          sb.String(),
				ProtectedData: out.EncryptedContent,
				ContentHeader: message.ContentHeader{
					RawRepresentation: out,
				},
			})

		case responses.ResponseFunctionToolCall:
			callID := cmp.Or(out.CallID, out.ID)
			currentUpdate.Contents = append(currentUpdate.Contents, &message.FunctionCallContent{
				CallID:    callID,
				Name:      out.Name,
				Arguments: out.Arguments,
				ContentHeader: message.ContentHeader{
					RawRepresentation: out,
				},
			})

		case responses.ResponseCodeInterpreterToolCall:
			var input message.CodeInterpreterToolCallContent
			input.CallID = out.ID
			if out.Code != "" {
				input.Inputs = []message.Content{
					&message.DataContent{
						Data:      base64.StdEncoding.EncodeToString([]byte(out.Code)),
						MediaType: "text/x-python",
					},
				}
			}
			currentUpdate.Contents = append(currentUpdate.Contents, &input)

			var output message.CodeInterpreterToolResultContent
			output.CallID = out.ID
			output.RawRepresentation = out
			for _, res := range out.Outputs {
				switch res := res.AsAny().(type) {
				case responses.ResponseCodeInterpreterToolCallOutputLogs:
					output.Outputs = append(output.Outputs, &message.TextContent{
						Text:          res.Logs,
						ContentHeader: message.ContentHeader{RawRepresentation: res},
					})
				case responses.ResponseCodeInterpreterToolCallOutputImage:
					output.Outputs = append(output.Outputs, &message.URIContent{
						URI:       res.URL,
						MediaType: imageURIToMediaType(res.URL),
						ContentHeader: message.ContentHeader{
							RawRepresentation: res,
						},
					})
				}
			}
			currentUpdate.Contents = append(currentUpdate.Contents, &output)

		case responses.ResponseOutputItemMcpApprovalRequest:
			currentUpdate.Contents = append(currentUpdate.Contents, mcpApprovalRequestContent(out))

		case responses.ResponseOutputItemImageGenerationCall:
			if content := imageGenerationContent(out); content != nil {
				currentUpdate.Contents = append(currentUpdate.Contents, content)
			}
		}
	}

	// Add any final data to the pending update (usage, errors), then yield it.
	// Add usage if present
	if usage := responsesUsageToContent(resp.Usage); usage != nil {
		currentUpdate.Contents = append(currentUpdate.Contents, usage)
	}
	// If there's an error in the response, add an ErrorContent
	if resp.Error.Message != "" {
		currentUpdate.Contents = append(currentUpdate.Contents, &message.ErrorContent{
			Message:   resp.Error.Message,
			ErrorCode: string(resp.Error.Code),
		})
	}
	yield(currentUpdate, nil)
}

func imageURIToMediaType(uri string) string {
	switch strings.ToLower(path.Ext(uri)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".webp":
		return "image/webp"
	default:
		return "image/*"
	}
}

func responsesFinishReason(resp *responses.Response) string {
	switch resp.Status {
	case responses.ResponseStatusCompleted:
		return "stop"
	case responses.ResponseStatusIncomplete:
		return resp.IncompleteDetails.Reason
	default:
		return ""
	}
}

// responsesUsageToContent converts a Response.Usage object to a UsageContent message
func responsesUsageToContent(usage responses.ResponseUsage) *message.UsageContent {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil
	}
	return &message.UsageContent{
		Details: message.UsageDetails{
			InputTokenCount:       usage.InputTokens,
			OutputTokenCount:      usage.OutputTokens,
			TotalTokenCount:       usage.TotalTokens,
			CachedInputTokenCount: usage.InputTokensDetails.CachedTokens,
			ReasoningTokenCount:   usage.OutputTokensDetails.ReasoningTokens,
		},
	}
}

// responsesProcessStreamingUpdate processes a streaming update from the Responses API
func responsesProcessStreamingUpdate(update responses.ResponseStreamEventUnion, responseID string, isBackground bool) (*agent.ResponseUpdate, error) {
	createUpdate := func(role message.Role, contents []message.Content) *agent.ResponseUpdate {
		u := &agent.ResponseUpdate{
			Role:              role,
			Contents:          contents,
			ResponseID:        responseID,
			RawRepresentation: update,
		}
		return u
	}

	// Handle different event types using AsAny()
	var u *agent.ResponseUpdate
	switch event := update.AsAny().(type) {
	case responses.ResponseCreatedEvent:
		u = createUpdate(message.RoleAssistant, nil)
		u.CreatedAt = time.Unix(int64(event.Response.CreatedAt), 0)
		u.ResponseID = event.Response.ID
		u.AdditionalProperties = responsesPopulateAdditionalProperties(&event.Response)
		if contToken := createContinuationToken(event.Response.ID, event.SequenceNumber, event.Response.Status, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}

	case responses.ResponseQueuedEvent:
		u = createUpdate(message.RoleAssistant, nil)
		u.CreatedAt = time.Unix(int64(event.Response.CreatedAt), 0)
		u.ResponseID = event.Response.ID
		u.AdditionalProperties = responsesPopulateAdditionalProperties(&event.Response)
		if contToken := createContinuationToken(event.Response.ID, event.SequenceNumber, event.Response.Status, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}

	case responses.ResponseInProgressEvent:
		u = createUpdate(message.RoleAssistant, nil)
		u.CreatedAt = time.Unix(int64(event.Response.CreatedAt), 0)
		u.ResponseID = event.Response.ID
		u.AdditionalProperties = responsesPopulateAdditionalProperties(&event.Response)
		if contToken := createContinuationToken(event.Response.ID, event.SequenceNumber, event.Response.Status, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}

	case responses.ResponseCompletedEvent:
		u = createUpdate(message.RoleAssistant, nil)
		u.CreatedAt = time.Unix(int64(event.Response.CreatedAt), 0)
		u.ResponseID = event.Response.ID
		u.FinishReason = responsesFinishReason(&event.Response)
		u.AdditionalProperties = responsesPopulateAdditionalProperties(&event.Response)
		// Add usage if present
		if usage := responsesUsageToContent(event.Response.Usage); usage != nil {
			u.Contents = []message.Content{usage}
		}

	case responses.ResponseIncompleteEvent:
		u = createUpdate(message.RoleAssistant, nil)
		u.CreatedAt = time.Unix(int64(event.Response.CreatedAt), 0)
		u.ResponseID = event.Response.ID
		u.FinishReason = responsesFinishReason(&event.Response)
		u.AdditionalProperties = responsesPopulateAdditionalProperties(&event.Response)
		if contToken := createContinuationToken(event.Response.ID, event.SequenceNumber, event.Response.Status, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}

	case responses.ResponseTextDeltaEvent:
		u = createUpdate(message.RoleAssistant, []message.Content{
			&message.TextContent{Text: event.Delta},
		})
		if contToken := createContinuationToken(responseID, event.SequenceNumber, responses.ResponseStatusInProgress, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}

	case responses.ResponseReasoningTextDeltaEvent:
		u = createUpdate(message.RoleAssistant, []message.Content{
			&message.TextReasoningContent{Text: event.Delta},
		})
		if contToken := createContinuationToken(responseID, event.SequenceNumber, responses.ResponseStatusInProgress, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}

	case responses.ResponseReasoningSummaryTextDeltaEvent:
		u = createUpdate(message.RoleAssistant, []message.Content{
			&message.TextReasoningContent{Text: event.Delta},
		})
		if contToken := createContinuationToken(responseID, event.SequenceNumber, responses.ResponseStatusInProgress, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}

	case responses.ResponseRefusalDoneEvent:
		u = createUpdate(message.RoleAssistant, []message.Content{
			&message.ErrorContent{
				Message:   event.Refusal,
				ErrorCode: "Refusal",
			},
		})
		if contToken := createContinuationToken(responseID, event.SequenceNumber, responses.ResponseStatusInProgress, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}

	case responses.ResponseOutputItemDoneEvent:
		// Create update for all output item done events
		u = createUpdate(message.RoleAssistant, nil)
		if contToken := createContinuationToken(responseID, event.SequenceNumber, responses.ResponseStatusInProgress, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}
		switch item := event.Item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			// For messages, only emit content if there are annotations that weren't in delta events
			// Delta events handle the text itself, but annotations only appear in done events
			hasAnnotations := false
			for _, c := range item.Content {
				if c, ok := c.AsAny().(responses.ResponseOutputText); ok && len(c.Annotations) > 0 {
					hasAnnotations = true
					break
				}
			}

			if hasAnnotations {
				// Parse message content with annotations
				for _, c := range item.Content {
					switch c := c.AsAny().(type) {
					case responses.ResponseOutputText:
						textContent := &message.TextContent{Text: c.Text}
						populateAnnotations(c.Annotations, textContent)
						u.Contents = append(u.Contents, textContent)
					case responses.ResponseOutputRefusal:
						u.Contents = append(u.Contents, &message.ErrorContent{
							Message:   c.Refusal,
							ErrorCode: "Refusal",
						})
					}
				}
			}
			// If no annotations, don't emit content (delta events already did)

		case responses.ResponseFunctionToolCall:
			// Add function call content
			callID := cmp.Or(item.CallID, item.ID)
			u.Contents = []message.Content{
				&message.FunctionCallContent{
					CallID:    callID,
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			}

		case responses.ResponseCodeInterpreterToolCall:
			// For code interpreter, create a text representation
			var outputText strings.Builder
			fmt.Fprintf(&outputText, "[Code Interpreter: %s]\n", item.ID)
			if item.Code != "" {
				outputText.WriteString(item.Code)
				outputText.WriteString("\n")
			}
			if len(item.Outputs) > 0 {
				outputText.WriteString("[Output]\n")
				for _, output := range item.Outputs {
					switch output := output.AsAny().(type) {
					case responses.ResponseCodeInterpreterToolCallOutputLogs:
						outputText.WriteString(output.Logs)
						outputText.WriteString("\n")
					case responses.ResponseCodeInterpreterToolCallOutputImage:
						if output.URL != "" {
							fmt.Fprintf(&outputText, "Image: %s\n", output.URL)
						}
					}
				}
			}
			content := &message.TextContent{Text: outputText.String()}
			content.RawRepresentation = item
			u.Contents = []message.Content{content}
		case responses.ResponseOutputItemMcpApprovalRequest:
			u.Contents = []message.Content{mcpApprovalRequestContent(item)}
		case responses.ResponseOutputItemImageGenerationCall:
			if content := imageGenerationContent(item); content != nil {
				u.Contents = []message.Content{content}
			}
		default:
			u = createUpdate(message.RoleAssistant, nil)
		}
	case responses.ResponseErrorEvent:
		u = createUpdate(message.RoleAssistant, []message.Content{
			&message.ErrorContent{
				Message:   event.Message,
				ErrorCode: event.Code,
				Details:   event.Param,
			},
		})
		if contToken := createContinuationToken(responseID, event.SequenceNumber, responses.ResponseStatusFailed, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}
	default:
		u = createUpdate(message.RoleAssistant, nil)
		if contToken := createContinuationToken(responseID, update.SequenceNumber, responses.ResponseStatusInProgress, isBackground); contToken != "" {
			u.ContinuationToken = contToken
		}
	}

	return u, nil
}

func mcpApprovalRequestContent(item responses.ResponseOutputItemMcpApprovalRequest) *message.ToolApprovalRequestContent {
	return &message.ToolApprovalRequestContent{
		ContentHeader: message.ContentHeader{RawRepresentation: item},
		RequestID:     item.ID,
		ToolCall: &message.MCPServerToolCallContent{
			ContentHeader: message.ContentHeader{RawRepresentation: item},
			Arguments:     item.Arguments,
			CallID:        item.ID,
			Name:          item.Name,
			ServerName:    item.ServerLabel,
		},
	}
}

func imageGenerationContent(item responses.ResponseOutputItemImageGenerationCall) *message.DataContent {
	if item.Result == "" {
		return nil
	}
	name := ""
	if item.ID != "" {
		name = item.ID + ".png"
	}
	return &message.DataContent{
		ContentHeader: message.ContentHeader{RawRepresentation: item},
		Data:          item.Result,
		MediaType:     "image/png",
		Name:          name,
	}
}

func populateAnnotations(anns []responses.ResponseOutputTextAnnotationUnion, content *message.TextContent) {
	for _, ann := range anns {
		switch a := ann.AsAny().(type) {
		case responses.ResponseOutputTextAnnotationFileCitation:
			content.Annotations = append(content.Annotations, &message.CitationAnnotation{
				FileID:            a.FileID,
				RawRepresentation: a,
			})
		case responses.ResponseOutputTextAnnotationURLCitation:
			content.Annotations = append(content.Annotations, &message.CitationAnnotation{
				URL:               a.URL,
				RawRepresentation: a,
			})
		}
	}
}

func responsesPopulateAdditionalProperties(resp *responses.Response) map[string]any {
	props := make(map[string]any)
	if resp.User != "" { //nolint:staticcheck // SA1019: no replacement available
		props["EndUserId"] = resp.User //nolint:staticcheck // SA1019: no replacement available
	}
	if resp.Error.Message != "" {
		props["Error"] = resp.Error.Message
	}
	return props
}

// createContinuationToken creates a continuation token if in background mode and status allows continuation
func createContinuationToken(responseID string, sequenceNumber int64, status responses.ResponseStatus, isBackground bool) string {
	if !isBackground {
		return ""
	}
	if status == responses.ResponseStatusCompleted || status == responses.ResponseStatusFailed || status == responses.ResponseStatusCancelled {
		return ""
	}
	ct := continuationToken{
		ResponseID:     responseID,
		SequenceNumber: sequenceNumber,
	}
	data, _ := json.Marshal(ct)
	return string(data)
}

type continuationToken struct {
	ResponseID     string `json:"response_id"`
	SequenceNumber int64  `json:"sequence_number"`
}
