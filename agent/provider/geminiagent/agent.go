// Copyright (c) Microsoft. All rights reserved.

package geminiagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/format"
	"github.com/microsoft/agent-framework-go/format/jsonformat"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"google.golang.org/genai"
)

type generateContentConfigOpt genai.GenerateContentConfig

func (o generateContentConfigOpt) Value() any { return genai.GenerateContentConfig(o) }

// GenerateContentConfig allows passing custom parameters to the underlying genai API calls.
func GenerateContentConfig(config genai.GenerateContentConfig) agentopt.Option {
	return generateContentConfigOpt(config)
}

type client struct {
	client *genai.Client
	config Config
}

// Config contains configuration for [New].
type Config struct {
	Model  string
	Client *genai.Client

	Agent agent.Config
}

// New creates a new [agent.Agent] backed by the Google Gemini API via the genai client.
// If Client is nil, [genai.NewClient] is called with nil config, which reads credentials
// from the GOOGLE_API_KEY or GEMINI_API_KEY environment variables.
func New(config Config) *agent.Agent {
	a, err := NewWithContext(context.Background(), config)
	if err != nil {
		panic(err)
	}
	return a
}

// NewWithContext is like [New], but allows passing a context when creating a default genai client.
func NewWithContext(ctx context.Context, config Config) (*agent.Agent, error) {
	if config.Client == nil {
		var err error
		config.Client, err = genai.NewClient(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("geminiagent: failed to create genai client: %w", err)
		}
	}
	c := &client{
		client: config.Client,
		config: config,
	}
	return agent.New(agent.ProviderConfig{
		Run:          c.run,
		ProviderName: "gemini",
		FormatOfFn:   c.formatOf,
		UnmarshalFn:  c.unmarshal,
	}, config.Agent), nil
}

func (a *client) formatOf(v any) (format.Format, error) {
	return jsonformat.ForType(reflect.TypeOf(v))
}

func (a *client) unmarshal(f format.Format, data []byte, v any) error {
	return jsonformat.Unmarshal(f.(*jsonformat.Format), data, v)
}

func (a *client) run(ctx context.Context, messages []*message.Message, options ...agentopt.Option) iter.Seq2[*message.ResponseUpdate, error] {
	contents, cfg, err := a.buildParams(messages, options)
	if err != nil {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}

	if stream, _ := agentopt.Get(options, agentopt.Stream); !stream {
		resp, err := a.client.Models.GenerateContent(ctx, a.config.Model, contents, cfg)
		if err != nil {
			return func(yield func(*message.ResponseUpdate, error) bool) {
				yield(nil, err)
			}
		}
		var responseContents []message.Content
		if len(resp.Candidates) > 0 {
			cand := resp.Candidates[0]
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					responseContents, err = buildResponsePart(part, responseContents)
					if err != nil {
						return func(yield func(*message.ResponseUpdate, error) bool) {
							yield(nil, err)
						}
					}
				}
			}
		}
		if resp.UsageMetadata != nil {
			responseContents = append(responseContents, &message.UsageContent{
				Details: toUsageDetails(resp.UsageMetadata),
			})
		}
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Contents:          responseContents,
				Role:              message.RoleAssistant,
				CreatedAt:         time.Now(),
				RawRepresentation: resp,
			}, nil)
		}
	}

	return func(yield func(*message.ResponseUpdate, error) bool) {
		for resp, err := range a.client.Models.GenerateContentStream(ctx, a.config.Model, contents, cfg) {
			if err != nil {
				yield(nil, err)
				return
			}
			var streamContents []message.Content
			if len(resp.Candidates) > 0 {
				cand := resp.Candidates[0]
				if cand.Content != nil {
					for _, part := range cand.Content.Parts {
						streamContents, err = buildResponsePart(part, streamContents)
						if err != nil {
							yield(nil, err)
							return
						}
					}
				}
			}
			if resp.UsageMetadata != nil {
				streamContents = append(streamContents, &message.UsageContent{
					Details: toUsageDetails(resp.UsageMetadata),
				})
			}
			if !yield(&message.ResponseUpdate{
				Contents:          streamContents,
				Role:              message.RoleAssistant,
				CreatedAt:         time.Now(),
				RawRepresentation: resp,
			}, nil) {
				return
			}
		}
	}
}

// buildParams converts framework messages and options into genai API parameters.
func (a *client) buildParams(messages []*message.Message, opts []agentopt.Option) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	cfg := &genai.GenerateContentConfig{}
	if p, ok := agentopt.Get(opts, GenerateContentConfig); ok {
		*cfg = p
		// Clone mutable slice fields so that appending to cfg.Tools or
		// cfg.SystemInstruction.Parts below never aliases the caller's
		// backing arrays (the option stores a shallow copy of the struct).
		cfg.Tools = append([]*genai.Tool(nil), cfg.Tools...)
		if cfg.SystemInstruction != nil {
			si := *cfg.SystemInstruction
			si.Parts = append([]*genai.Part(nil), si.Parts...)
			cfg.SystemInstruction = &si
		}
	}

	// Collect tools from options.
	var funcDecls []*genai.FunctionDeclaration
	for tl := range agentopt.All(opts, agentopt.Tool) {
		if ft, ok := tl.(tool.FuncTool); ok {
			decl := &genai.FunctionDeclaration{
				Name:        ft.Name(),
				Description: ft.Description(),
			}
			if schema := ft.Schema(); schema != nil {
				// Use ParametersJsonSchema to pass through the JSON schema directly.
				decl.ParametersJsonSchema = schema
			}
			funcDecls = append(funcDecls, decl)
		}
	}
	if len(funcDecls) > 0 {
		cfg.Tools = append(cfg.Tools, &genai.Tool{
			FunctionDeclarations: funcDecls,
		})
	}

	// Apply structured output format.
	if frmt, ok := agentopt.Get(opts, agentopt.ResponseFormat); ok && frmt != nil {
		if frmt.Kind() == "json" {
			cfg.ResponseMIMEType = "application/json"
			if schemaFmt, ok := frmt.(format.SchemaFormat); ok {
				if schema := schemaFmt.Schema(); schema != nil {
					cfg.ResponseJsonSchema = schema
				}
			}
		}
	}

	// Apply tool mode.
	if mode, ok := agentopt.Get(opts, agentopt.ToolMode); ok && len(funcDecls) > 0 {
		fc := &genai.FunctionCallingConfig{}
		switch mode.Mode() {
		case tool.ToolModeAuto, "":
			fc.Mode = genai.FunctionCallingConfigModeAuto
		case tool.ToolModeNone:
			fc.Mode = genai.FunctionCallingConfigModeNone
		case tool.ToolModeRequired:
			fc.Mode = genai.FunctionCallingConfigModeAny
			fc.AllowedFunctionNames = mode.Required()
		}
		cfg.ToolConfig = &genai.ToolConfig{
			FunctionCallingConfig: fc,
		}
	}

	// Build a map of CallID → function name by scanning all messages first.
	// This is needed because FunctionResultContent doesn't store the function name,
	// but genai's FunctionResponse requires it to match the FunctionDeclaration.
	callIDToName := make(map[string]string)
	for _, msg := range messages {
		for _, c := range msg.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok && fc.CallID != "" && fc.Name != "" {
				callIDToName[fc.CallID] = fc.Name
			}
		}
	}

	// Build contents from messages.
	var contents []*genai.Content
	for _, msg := range messages {
		switch msg.Role {
		case message.RoleSystem:
			// Gemini uses a single system instruction content that can hold multiple parts.
			// Add each non-empty system text content as its own part.
			if cfg.SystemInstruction == nil {
				cfg.SystemInstruction = &genai.Content{Role: genai.RoleUser}
			}
			for _, c := range msg.Contents {
				if tc, ok := c.(*message.TextContent); ok && tc.Text != "" {
					cfg.SystemInstruction.Parts = append(cfg.SystemInstruction.Parts, &genai.Part{Text: tc.Text})
				}
			}
		case message.RoleUser, message.RoleTool:
			parts, err := buildRequestParts(msg, callIDToName)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  genai.RoleUser,
					Parts: parts,
				})
			}
		case message.RoleAssistant:
			parts, err := buildRequestParts(msg, callIDToName)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  genai.RoleModel,
					Parts: parts,
				})
			}
		}
	}

	return contents, cfg, nil
}

// buildRequestParts converts a framework message's contents into genai Parts.
// callIDToName maps function call IDs to function names, used to populate
// FunctionResponse.Name which genai requires but FunctionResultContent doesn't store.
func buildRequestParts(msg *message.Message, callIDToName map[string]string) ([]*genai.Part, error) {
	var parts []*genai.Part
	for _, c := range msg.Contents {
		switch c := c.(type) {
		case *message.TextContent:
			if c.Text != "" {
				parts = append(parts, &genai.Part{Text: c.Text})
			}
		case *message.TextReasoningContent:
			// Pass thought blocks back to the model in multi-turn conversations.
			part := &genai.Part{Thought: true, Text: c.Text}
			if c.ProtectedData != "" {
				sig, err := base64.StdEncoding.DecodeString(c.ProtectedData)
				if err != nil {
					return nil, fmt.Errorf("geminiagent: failed to decode reasoning protected data: %w", err)
				}
				part.ThoughtSignature = sig
			}
			parts = append(parts, part)
		case *message.FunctionCallContent:
			var args map[string]any
			if c.Arguments != "" {
				if err := json.Unmarshal([]byte(c.Arguments), &args); err != nil {
					return nil, fmt.Errorf("geminiagent: failed to unmarshal function call arguments: %w", err)
				}
			}
			parts = append(parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   c.CallID,
					Name: c.Name,
					Args: args,
				},
			})
		case *message.FunctionResultContent:
			name, ok := callIDToName[c.CallID]
			if c.CallID == "" || !ok || name == "" {
				return nil, fmt.Errorf("geminiagent: missing function name for result with call ID %q", c.CallID)
			}
			response, err := toFunctionResponseMap(c)
			if err != nil {
				return nil, err
			}
			parts = append(parts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					ID:       c.CallID,
					Name:     name,
					Response: response,
				},
			})
		case *message.DataContent:
			data, err := c.Bytes()
			if err != nil {
				return nil, fmt.Errorf("geminiagent: failed to decode data content: %w", err)
			}
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{
					Data:     data,
					MIMEType: c.MediaType,
				},
			})
		}
	}
	return parts, nil
}

// buildResponsePart converts a genai Part from a response into framework message content.
func buildResponsePart(part *genai.Part, contents []message.Content) ([]message.Content, error) {
	if part.Thought {
		// Thinking model: emit TextReasoningContent. Encode ThoughtSignature as
		// base64 in ProtectedData so it can be passed back in multi-turn requests.
		protectedData := ""
		if len(part.ThoughtSignature) > 0 {
			protectedData = base64.StdEncoding.EncodeToString(part.ThoughtSignature)
		}
		contents = append(contents, &message.TextReasoningContent{
			Text:          part.Text,
			ProtectedData: protectedData,
			ContentHeader: message.ContentHeader{
				RawRepresentation: part,
			},
		})
	} else if part.Text != "" {
		contents = append(contents, &message.TextContent{
			Text: part.Text,
			ContentHeader: message.ContentHeader{
				RawRepresentation: part,
			},
		})
	}
	if part.FunctionCall != nil {
		args := part.FunctionCall.Args
		if args == nil {
			args = map[string]any{}
		}
		argsJSON, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("geminiagent: failed to marshal function call arguments: %w", err)
		}
		contents = append(contents, &message.FunctionCallContent{
			CallID:    part.FunctionCall.ID,
			Name:      part.FunctionCall.Name,
			Arguments: string(argsJSON),
			ContentHeader: message.ContentHeader{
				RawRepresentation: part,
			},
		})
	}
	return contents, nil
}

// toFunctionResponseMap converts a FunctionResultContent's result to the map[string]any
// format required by genai.
func toFunctionResponseMap(c *message.FunctionResultContent) (map[string]any, error) {
	if c.Error != nil {
		return map[string]any{"error": c.Error.Error()}, nil
	}
	switch r := c.Result.(type) {
	case map[string]any:
		return r, nil
	case json.RawMessage:
		var m map[string]any
		if err := json.Unmarshal(r, &m); err != nil {
			return map[string]any{"output": string(r)}, nil
		}
		return m, nil
	case string:
		return map[string]any{"output": r}, nil
	case []byte:
		return map[string]any{"output": string(r)}, nil
	default:
		data, err := json.Marshal(c.Result)
		if err != nil {
			return nil, fmt.Errorf("geminiagent: failed to marshal function result: %w", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return map[string]any{"output": string(data)}, nil
		}
		return m, nil
	}
}

func toUsageDetails(u *genai.GenerateContentResponseUsageMetadata) message.UsageDetails {
	return message.UsageDetails{
		InputTokenCount:       int64(u.PromptTokenCount),
		OutputTokenCount:      int64(u.CandidatesTokenCount),
		TotalTokenCount:       int64(u.TotalTokenCount),
		CachedInputTokenCount: int64(u.CachedContentTokenCount),
	}
}
